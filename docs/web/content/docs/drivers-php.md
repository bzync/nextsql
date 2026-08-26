# PHP driver

PHP 8.1+. Path: [`drivers/php`](https://github.com/bzync/nextsql/tree/main/drivers/php).

```php
require 'drivers/php/autoload.php';

$conn = NextSQL\Client::connect([
    'address' => '127.0.0.1:7210',
    'user' => 'app',
    'password' => getenv('NEXTSQL_PASSWORD'),
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
    'password' => getenv('NEXTSQL_PASSWORD'),
    'tls' => ['cafile' => '/etc/nextsql/ca.pem', 'servername' => 'db.example.com'],
]);
```

For `--require-client-key`, pass `'key' => $clientRoot` as a 32-byte string. Never put keys or passwords in a URL.
