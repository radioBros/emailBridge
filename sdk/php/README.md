# `emailbridge/sdk` (PHP)

PSR-18 HTTP client wrapper, Standard Webhooks / `bodyOnly` verifier, and SSE helper.

Requires PHP ≥ 8.2 and a PSR-17 factory + PSR-18 client (e.g. Guzzle + Nyholm PSR-7).

## Install

```bash
composer require emailbridge/sdk
```

## Quick start

```php
use EmailBridge\Sdk\Client;
use EmailBridge\Sdk\Webhook;
use GuzzleHttp\Client as GuzzleClient;
use Nyholm\Psr7\Factory\Psr17Factory;

$factory = new Psr17Factory();
$http = new GuzzleClient(['http_errors' => false]);
$client = new Client(
    'https://mail.example.com',
    getenv('EB_API_KEY'),
    $http,
    $factory,
    $factory,
    'org_…',
);

$page = $client->get('/v1/accounts', ['limit' => 50]);
foreach ($client->iterateCursor('/v1/accounts', [], 100) as $row) {
    // …
}

Webhook::verify($secret, $request->getHeaders(), $rawBody);
```

## Tests

```bash
composer install
composer test
```

## Regenerate

```bash
./scripts/generate
```
