# PHP driver

PHP 8.1+ (requires `ext-openssl`; zero Composer runtime dependencies). MIT. Package version **0.0.1**. Driver package versions are independent of the engine version.

```bash
composer require bzync/nextsql
```

The Composer package is `bzync/nextsql`, namespace `NextSQL\`. Source: [`drivers/php`](https://github.com/bzync/nextsql/tree/master/drivers/php) — you can also vendor it directly from the repository with its bundled `autoload.php`.

---

## Quick start

```php
<?php
require 'vendor/autoload.php'; // or 'drivers/php/autoload.php' when vendored

$conn = NextSQL\Client::connect([
    'address'       => '127.0.0.1:7210',
    'database'      => 'default',
    'user'          => 'app',
    'password'      => getenv('NEXTSQL_DATABASE_PASS'),
    'insecureNoTLS' => true, // loopback only
]);

$res = $conn->exec('SELECT name FROM items WHERE price < $1', [
    ['kind' => 'decimal', 'value' => '50.00'],
]);
foreach ($res['rows'] as $row) {
    print_r($row);
}
$conn->close();
```

### Remote TLS & client unlock key

```php
$conn = NextSQL\Client::connect([
    'address'  => 'db.example.com:7210',
    'database' => 'production',
    'user'     => 'app',
    'password' => getenv('NEXTSQL_DATABASE_PASS'),
    'tls'      => [
        'cafile'     => '/etc/nextsql/ca.pem',
        'servername' => 'db.example.com',
    ],
]);
```

For `--require-client-key`, pass `'key' => $clientRoot` as a 32-byte binary string. Never put keys or passwords in a URL.

---

## Connection pooling & prepared statement LRU cache

Both `NextSQL\Client` and `NextSQL\Pool` include a client-side **LRU prepared statement cache** (`statementCacheSize: 50` by default; `0` disables caching). Parameterized queries reuse server-side prepared statements and automatically close evicted handles on the server.

```php
$pool = new NextSQL\Pool($config, minSize: 2, maxSize: 10, statementCacheSize: 50);

// Direct shorthand query (acquires and releases connection automatically):
$rows = $pool->query('SELECT id, name FROM users WHERE status = $1', ['active']);

// Scoped checkout with guaranteed release on exception:
$pool->withConnection(function (NextSQL\Client $conn) {
    $conn->exec('INSERT INTO audit_log (msg) VALUES ($1)', ['started']);
});

$pool->close();
```

---

## Client-side field encryption

Pass a `FieldKeyProvider` (`MemoryFieldKeyring` or durable `FileFieldKeyring`) as `'fieldKeys'`. Use `encryptField` / `decryptField` for randomized `NSCE1` columns, and `encryptFieldDeterministic` / `decryptFieldDeterministic` for `ENCRYPTED CLIENT DETERMINISTIC` (`NSCE2`) equality predicates:

```php
$keyring = new NextSQL\FileFieldKeyring('/etc/nextsql/field-keys.json');
$conn = NextSQL\Client::connect([
    'address'       => '127.0.0.1:7210',
    'database'      => 'default',
    'user'          => 'app',
    'password'      => getenv('NEXTSQL_DATABASE_PASS'),
    'insecureNoTLS' => true,
    'fieldKeys'     => $keyring,
]);

$ct = $conn->encryptField('patients', 'ssn', '123-45-6789');
$conn->exec('INSERT INTO patients (id, ssn) VALUES ($1, $2)', [1, $ct]);
```

See [Client-side encryption](/docs/client-encryption) for details.

---

## Cluster routing & follower reads

Follower-read routing uses `NextSQL\Cluster::connect` with `READ_STRONG`, `READ_BOUNDED`, or `READ_STALE`. See [High availability](/docs/ha) and [Error codes & taxonomy](/docs/error-codes).
