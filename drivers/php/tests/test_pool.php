<?php

declare(strict_types=1);

require dirname(__DIR__) . '/autoload.php';

use NextSQL\Client;
use NextSQL\Exception;
use NextSQL\Pool;

function fail(string $msg): never
{
    fwrite(STDERR, $msg . "\n");
    exit(1);
}

function expectException(callable $fn, string $code): void
{
    try {
        $fn();
        fail('expected exception ' . $code);
    } catch (Exception $e) {
        if ($e->errorCode !== $code) {
            fail('got ' . $e->errorCode . ' want ' . $code);
        }
    }
}

// 1. Invalid pool parameters
$dummyCfg = [
    'address' => '127.0.0.1:7210',
    'database' => 'test',
    'user' => 'app',
    'password' => 'secret',
    'insecureNoTLS' => true,
];

expectException(static fn () => new Pool($dummyCfg, -1), 'invalid_argument');
expectException(static fn () => new Pool($dummyCfg, 5, 2), 'invalid_argument');
expectException(static fn () => new Pool($dummyCfg, 0, 0), 'invalid_argument');
expectException(static fn () => new Pool($dummyCfg, 0, 5, 5.0, 300.0, -1), 'invalid_argument');

// 2. Pool lifecycle with min_size = 0
$pool = new Pool($dummyCfg, 0, 3, 0.05);
if ($pool->getSize() !== 0) {
    fail('expected size 0, got ' . $pool->getSize());
}
if ($pool->getIdleCount() !== 0) {
    fail('expected idle 0, got ' . $pool->getIdleCount());
}
if ($pool->getInUseCount() !== 0) {
    fail('expected in-use 0, got ' . $pool->getInUseCount());
}
if ($pool->isClosed()) {
    fail('expected not closed');
}

$pool->close();
if (!$pool->isClosed()) {
    fail('expected closed');
}

expectException(static fn () => $pool->acquire(), 'unavailable');

// 3. Live tests when NEXTSQL_ADDR is present
$addr = getenv('NEXTSQL_ADDR');
$ca = getenv('NEXTSQL_CA');

if ($addr !== false && $addr !== '') {
    $cfg = [
        'address' => $addr,
        'database' => 'production',
        'user' => getenv('NEXTSQL_DATABASE_USER') ?: 'app',
        'password' => getenv('NEXTSQL_DATABASE_PASS') ?: 's3cret',
    ];
    if ($ca !== false && $ca !== '') {
        $cfg['tls'] = [
            'cafile' => $ca,
            'servername' => 'localhost',
        ];
    } else {
        $cfg['insecureNoTLS'] = true;
    }

    $livePool = new Pool($cfg, 2, 5, 5.0);
    if ($livePool->getSize() !== 2) {
        fail('expected live pool prewarmed size 2, got ' . $livePool->getSize());
    }
    if ($livePool->getIdleCount() !== 2) {
        fail('expected idle 2, got ' . $livePool->getIdleCount());
    }

    // Acquire 1
    $c1 = $livePool->acquire();
    if ($livePool->getIdleCount() !== 1 || $livePool->getInUseCount() !== 1) {
        fail('idle/in-use mismatch after acquire');
    }

    // Acquire 2
    $c2 = $livePool->acquire();
    if ($livePool->getIdleCount() !== 0 || $livePool->getInUseCount() !== 2) {
        fail('idle/in-use mismatch after acquire 2');
    }

    // Release both
    $livePool->release($c1);
    $livePool->release($c2);
    if ($livePool->getIdleCount() !== 2 || $livePool->getInUseCount() !== 0) {
        fail('idle/in-use mismatch after release');
    }

    // Execute via pool
    $res = $livePool->exec('SELECT 1 AS num');
    if (count($res['rows']) !== 1 || $res['rows'][0][0] !== 1) {
        fail('pool exec returned unexpected rows');
    }

    $qres = $livePool->query('SELECT 42 AS answer');
    if (count($qres['rows']) !== 1 || $qres['rows'][0][0] !== 42) {
        fail('pool query returned unexpected rows');
    }

    // Transaction cleanup on release
    $livePool->withConnection(static function (Client $conn): void {
        $conn->exec('BEGIN');
        $conn->exec('CREATE TABLE IF NOT EXISTS test_pool_txn (id INT)');
        // leave txn open without COMMIT
    });

    // Next connection must be clean (not in transaction)
    $livePool->withConnection(static function (Client $conn): void {
        $res = $conn->exec('SELECT 1');
        if (count($res['rows']) !== 1) {
            fail('failed after uncommitted transaction cleanup');
        }
    });

    $livePool->close();
}

fwrite(STDOUT, "ok\n");
