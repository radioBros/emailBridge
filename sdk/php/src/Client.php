<?php

declare(strict_types=1);

namespace EmailBridge\Sdk;

use Psr\Http\Client\ClientInterface;
use Psr\Http\Message\RequestFactoryInterface;
use Psr\Http\Message\StreamFactoryInterface;

final class Client
{
    private readonly string $baseUrl;

    public function __construct(
        string $baseUrl,
        private readonly string $apiKey,
        private readonly ClientInterface $http,
        private readonly RequestFactoryInterface $requestFactory,
        private readonly StreamFactoryInterface $streamFactory,
        private readonly ?string $organizationId = null,
        private readonly int $maxRetries = 3,
    ) {
        $this->baseUrl = rtrim($baseUrl, '/');
    }

    public function getApiKey(): string
    {
        return $this->apiKey;
    }

    public function getOrganizationId(): ?string
    {
        return $this->organizationId;
    }

    public function getBaseUrl(): string
    {
        return $this->baseUrl;
    }

    /**
     * @param array<string, scalar|null> $query
     * @param array<string, string> $headers
     * @return mixed
     */
    public function request(
        string $method,
        string $path,
        array $query = [],
        mixed $body = null,
        array $headers = [],
        ?string $idempotencyKey = null,
    ): mixed {
        $url = $this->baseUrl . '/' . ltrim($path, '/');
        if ($query !== []) {
            $url .= (str_contains($url, '?') ? '&' : '?') . http_build_query($query);
        }

        $hdrs = array_merge([
            'Accept' => 'application/json',
            'Authorization' => 'Bearer ' . $this->apiKey,
        ], $headers);
        if ($this->organizationId !== null && !isset($hdrs['EB-Organization'])) {
            $hdrs['EB-Organization'] = $this->organizationId;
        }
        if ($idempotencyKey !== null) {
            $hdrs['Idempotency-Key'] = $idempotencyKey;
        }

        $payload = null;
        if ($body !== null) {
            $payload = is_string($body) ? $body : json_encode($body, JSON_THROW_ON_ERROR);
            $hdrs['Content-Type'] ??= 'application/json';
        }

        for ($attempt = 0; $attempt <= $this->maxRetries; $attempt++) {
            $req = $this->requestFactory->createRequest($method, $url);
            foreach ($hdrs as $k => $v) {
                $req = $req->withHeader($k, $v);
            }
            if ($payload !== null) {
                $req = $req->withBody($this->streamFactory->createStream($payload));
            }
            $res = $this->http->sendRequest($req);
            $status = $res->getStatusCode();

            if (($status === 429 || $status === 503) && $attempt < $this->maxRetries) {
                usleep((int) ($this->retryAfterUs($res->getHeaderLine('Retry-After'), $attempt)));
                continue;
            }

            $raw = (string) $res->getBody();
            if ($status === 204 || $status === 205) {
                return null;
            }
            $parsed = $raw === '' ? null : json_decode($raw, true);
            if ($status >= 400) {
                $flat = [];
                foreach ($res->getHeaders() as $k => $vals) {
                    $flat[$k] = $vals[0] ?? '';
                }
                throw new ProblemException(Problem::fromBody($parsed, $status), $flat);
            }
            return $parsed;
        }

        throw new \RuntimeException('request failed');
    }

    /** @param array<string, scalar|null> $query */
    public function get(string $path, array $query = [], array $headers = []): mixed
    {
        return $this->request('GET', $path, $query, null, $headers);
    }

    /** @param array<string, scalar|null> $query */
    public function post(string $path, mixed $body = null, array $query = [], array $headers = [], ?string $idempotencyKey = null): mixed
    {
        return $this->request('POST', $path, $query, $body, $headers, $idempotencyKey);
    }

    /** @param array<string, scalar|null> $query */
    public function patch(string $path, mixed $body = null, array $query = [], array $headers = []): mixed
    {
        return $this->request('PATCH', $path, $query, $body, $headers);
    }

    /** @param array<string, scalar|null> $query */
    public function delete(string $path, array $query = [], array $headers = []): mixed
    {
        return $this->request('DELETE', $path, $query, null, $headers);
    }

    /**
     * @param array<string, scalar|null> $query
     * @return \Generator<int, mixed>
     */
    public function iterateCursor(string $path, array $query = [], ?int $limit = null): \Generator
    {
        if ($limit !== null) {
            $query['limit'] = $limit;
        }
        $cursor = $query['cursor'] ?? null;
        unset($query['cursor']);
        while (true) {
            $q = $query;
            if ($cursor !== null && $cursor !== '') {
                $q['cursor'] = $cursor;
            }
            $page = $this->get($path, $q);
            foreach ($page['data'] ?? [] as $item) {
                yield $item;
            }
            $cursor = $page['nextCursor'] ?? null;
            if ($cursor === null || $cursor === '') {
                return;
            }
        }
    }

    private function retryAfterUs(string $retryAfter, int $attempt): int
    {
        if ($retryAfter !== '') {
            if (is_numeric($retryAfter)) {
                return (int) (max(0, (float) $retryAfter) * 1_000_000);
            }
            $when = strtotime($retryAfter);
            if ($when !== false) {
                return (int) (max(0, $when - time()) * 1_000_000);
            }
        }
        return (int) (min(30_000, 500 * (2 ** $attempt)) * 1000);
    }
}
