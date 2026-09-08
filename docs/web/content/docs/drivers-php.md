# PHP driver

PHP 8.1+ (requires `ext-openssl`). MIT, versioned independently of the engine
(package `0.1.0`).

```bash
composer require bzync/nextsql
```

The Composer package is `bzync/nextsql`, namespace `NextSQL\`. Source:
[`drivers/php`](https://github.com/bzync/nextsql/tree/master/drivers/php) — you can
also vendor it straight from the tree with its bundled `autoload.php`.

```php
require 'vendor/autoload.php'; // or 'drivers/php/autoload.php' when vendored

$conn = NextSQL\Client::connect([
    'address' => '127.0.0.1:7210',
    'realm' => 'default',
    'database' => 'default',
    'user' => 'app',
    'password' => getenv('NEXTSQL_DATABASE_PASS'),
    'insecureNoTLS' => true,
]);

$res = $conn->exec('SELECT name FROM items WHERE price < $1', [
    ['kind' => 'decimal', 'value' => '50.00'],
]);
$conn->close();
```

Remote TLS:

```php
$conn = NextSQL\Client::connect([
    'address' => 'db.example.com:7210',
    'user' => 'app',
    'password' => getenv('NEXTSQL_DATABASE_PASS'),
    'tls' => ['cafile' => '/etc/nextsql/ca.pem', 'servername' => 'db.example.com'],
]);
```

For `--require-client-key`, pass `'key' => $clientRoot` as a 32-byte string. Never put keys or passwords in a URL.

Follower-read routing uses `NextSQL\Cluster::connect`. See [High availability](/docs/ha). Hosted connections take `'realm'` and `'database'`.
