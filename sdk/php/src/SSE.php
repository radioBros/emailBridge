<?php

declare(strict_types=1);

namespace EmailBridge\Sdk;

use Psr\Http\Client\ClientInterface;
use Psr\Http\Message\RequestFactoryInterface;

final class SSEEvent
{
    public function __construct(
        public readonly ?string $id,
        public readonly ?string $event,
        public readonly string $data,
    ) {
    }
}

final class SSE
{
    /**
     * Consume GET /v1/events/stream. Invokes $onEvent for each event.
     *
     * @param callable(SSEEvent): void $onEvent
     * @param array<string, scalar|null> $query
     */
    public static function stream(
        Client $client,
        ClientInterface $http,
        RequestFactoryInterface $requestFactory,
        callable $onEvent,
        array $query = [],
        ?string $lastEventId = null,
    ): void {
        $url = $client->getBaseUrl() . '/v1/events/stream';
        if ($query !== []) {
            $url .= '?' . http_build_query($query);
        }
        $req = $requestFactory->createRequest('GET', $url)
            ->withHeader('Accept', 'text/event-stream')
            ->withHeader('Authorization', 'Bearer ' . $client->getApiKey());
        if ($client->getOrganizationId() !== null) {
            $req = $req->withHeader('EB-Organization', $client->getOrganizationId());
        }
        if ($lastEventId !== null) {
            $req = $req->withHeader('Last-Event-ID', $lastEventId);
        }
        $res = $http->sendRequest($req);
        if ($res->getStatusCode() >= 400) {
            throw new \RuntimeException('SSE stream failed: ' . $res->getStatusCode());
        }
        $buf = '';
        $body = $res->getBody();
        while (!$body->eof()) {
            $buf .= $body->read(8192);
            while (($sep = strpos($buf, "\n\n")) !== false) {
                $block = str_replace("\r", '', substr($buf, 0, $sep));
                $buf = substr($buf, $sep + 2);
                $ev = self::parseBlock($block);
                if ($ev !== null) {
                    $onEvent($ev);
                }
            }
        }
    }

    private static function parseBlock(string $block): ?SSEEvent
    {
        $id = null;
        $event = null;
        $data = [];
        foreach (explode("\n", $block) as $line) {
            if ($line === '' || str_starts_with($line, ':')) {
                continue;
            }
            $pos = strpos($line, ':');
            if ($pos === false) {
                $field = $line;
                $value = '';
            } else {
                $field = substr($line, 0, $pos);
                $value = substr($line, $pos + 1);
                if (str_starts_with($value, ' ')) {
                    $value = substr($value, 1);
                }
            }
            if ($field === 'id') {
                $id = $value;
            } elseif ($field === 'event') {
                $event = $value;
            } elseif ($field === 'data') {
                $data[] = $value;
            }
        }
        if ($id === null && $event === null && $data === []) {
            return null;
        }
        return new SSEEvent($id, $event, implode("\n", $data));
    }
}
