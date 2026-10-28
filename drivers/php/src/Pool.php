<?php

declare(strict_types=1);

namespace NextSQL;

/**
 * Thread-safe connection pool for the NextSQL PHP driver.
 * Speaks the native NSQL v1 protocol. Keys and passwords never go in URLs.
 */
final class Pool
{
    /** @var array<string, mixed> */
    private array $cfg;
    private int $minSize;
    private int $maxSize;
    private float $timeout;
    private float $maxIdleSeconds;
    private int $statementCacheSize;

    /** @var list<array{client: Client, last_used: float, in_txn: bool}> */
    private array $idle = [];

    /** @var array<int, array{client: Client, last_used: float, in_txn: bool}> */
    private array $inUse = [];

    private bool $closed = false;

    /**
     * @param array<string, mixed> $cfg
     */
    public function __construct(
        array $cfg,
        int $minSize = 1,
        int $maxSize = 10,
        float $timeout = 5.0,
        float $maxIdleSeconds = 300.0,
        int $statementCacheSize = 50
    ) {
        if ($minSize < 0) {
            throw new Exception('invalid_argument', 'min_size must be >= 0');
        }
        if ($maxSize < 1) {
            throw new Exception('invalid_argument', 'max_size must be >= 1');
        }
        if ($maxSize < $minSize) {
            throw new Exception('invalid_argument', 'max_size must be >= min_size');
        }
        if ($statementCacheSize < 0) {
            throw new Exception('invalid_argument', 'statement_cache_size must be >= 0');
        }

        $cfg['statementCacheSize'] = $statementCacheSize;
        $this->cfg = $cfg;
        $this->minSize = $minSize;
        $this->maxSize = $maxSize;
        $this->timeout = $timeout;
        $this->maxIdleSeconds = $maxIdleSeconds;
        $this->statementCacheSize = $statementCacheSize;

        if ($minSize > 0) {
            for ($i = 0; $i < $minSize; $i++) {
                try {
                    $client = Client::connect($cfg);
                    $this->idle[] = [
                        'client' => $client,
                        'last_used' => microtime(true),
                        'in_txn' => false,
                    ];
                } catch (\Throwable $e) {
                    $this->close();
                    throw $e;
                }
            }
        }
    }

    public function getSize(): int
    {
        return count($this->idle) + count($this->inUse);
    }

    public function getIdleCount(): int
    {
        return count($this->idle);
    }

    public function getInUseCount(): int
    {
        return count($this->inUse);
    }

    public function isClosed(): bool
    {
        return $this->closed;
    }

    public function acquire(?float $timeout = null): Client
    {
        $waitTime = $timeout ?? $this->timeout;
        $deadline = $waitTime > 0 ? (microtime(true) + $waitTime) : (microtime(true) + 3600.0);

        while (true) {
            if ($this->closed) {
                throw new Exception('unavailable', 'connection pool is closed');
            }

            $now = microtime(true);
            while (count($this->idle) > 0) {
                $item = array_pop($this->idle);
                if ($this->maxIdleSeconds > 0 && ($now - $item['last_used']) > $this->maxIdleSeconds) {
                    $this->safeClose($item['client']);
                    continue;
                }
                if (!$this->isAlive($item['client'])) {
                    $this->safeClose($item['client']);
                    continue;
                }
                $id = spl_object_id($item['client']);
                $this->inUse[$id] = $item;
                return $item['client'];
            }

            if (($this->getSize()) < $this->maxSize) {
                $client = Client::connect($this->cfg);
                $item = [
                    'client' => $client,
                    'last_used' => microtime(true),
                    'in_txn' => false,
                ];
                $id = spl_object_id($client);
                $this->inUse[$id] = $item;
                return $client;
            }

            $remaining = $deadline - microtime(true);
            if ($remaining <= 0) {
                throw new Exception('resource_exhausted', 'connection pool exhausted, wait timeout exceeded');
            }

            usleep(min((int)($remaining * 1_000_000), 50_000));
        }
    }

    public function release(Client $client): void
    {
        $id = spl_object_id($client);
        if (!isset($this->inUse[$id])) {
            return;
        }

        $item = $this->inUse[$id];
        unset($this->inUse[$id]);

        if ($this->closed) {
            $this->safeClose($client);
            return;
        }

        if (!$this->cleanConnection($item)) {
            $this->safeClose($client);
            return;
        }

        $item['last_used'] = microtime(true);
        $item['in_txn'] = false;
        $this->idle[] = $item;
    }

    /**
     * @template T
     * @param callable(Client): T $fn
     * @return T
     */
    public function withConnection(callable $fn, ?float $timeout = null): mixed
    {
        $client = $this->acquire($timeout);
        try {
            return $fn($client);
        } finally {
            $this->release($client);
        }
    }

    /**
     * Executes a statement on a pooled connection.
     *
     * @param array<mixed> $params
     * @return array<string, mixed>
     */
    public function exec(string $sql, array $params = []): array
    {
        return $this->withConnection(static function (Client $conn) use ($sql, $params): array {
            return $conn->exec($sql, $params);
        });
    }

    /**
     * Executes a query on a pooled connection and collects all rows before
     * returning the connection to the pool.
     *
     * @param array<mixed> $params
     * @return array{columns: list<string>, rows: list<list<mixed>>, affected: int}
     */
    public function query(string $sql, array $params = []): array
    {
        return $this->withConnection(static function (Client $conn) use ($sql, $params): array {
            $rows = $conn->query($sql, $params);
            return $rows->collect();
        });
    }

    public function close(): void
    {
        if ($this->closed) {
            return;
        }
        $this->closed = true;

        foreach ($this->idle as $item) {
            $this->safeClose($item['client']);
        }
        $this->idle = [];

        foreach ($this->inUse as $item) {
            $this->safeClose($item['client']);
        }
        $this->inUse = [];
    }

    private function safeClose(Client $client): void
    {
        try {
            $client->close();
        } catch (\Throwable) {
        }
    }

    private function isAlive(Client $client): bool
    {
        try {
            $client->nodeStatus();
            return true;
        } catch (\Throwable) {
            return false;
        }
    }

    /**
     * @param array{client: Client, last_used: float, in_txn: bool} $item
     */
    private function cleanConnection(array $item): bool
    {
        try {
            $item['client']->exec('ROLLBACK;');
            return true;
        } catch (\Throwable) {
            return false;
        }
    }
}
