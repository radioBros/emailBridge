<?php

declare(strict_types=1);

namespace EmailBridge\Sdk\Tests;

use EmailBridge\Sdk\Client;
use EmailBridge\Sdk\ProblemException;
use GuzzleHttp\Handler\MockHandler;
use GuzzleHttp\HandlerStack;
use GuzzleHttp\Middleware;
use GuzzleHttp\Psr7\Response;
use GuzzleHttp\Client as GuzzleClient;
use Nyholm\Psr7\Factory\Psr17Factory;
use PHPUnit\Framework\TestCase;

final class ClientTest extends TestCase
{
    /** @var list<array{request: \Psr\Http\Message\RequestInterface}> */
    private array $history = [];

    /** @param list<Response> $responses */
    private function makeClient(array $responses, ?string $org = null): Client
    {
        $this->history = [];
        $mock = new MockHandler($responses);
        $stack = HandlerStack::create($mock);
        $stack->push(Middleware::history($this->history));
        $factory = new Psr17Factory();
        $http = new GuzzleClient(['handler' => $stack, 'http_errors' => false]);
        return new Client('http://eb.test', 'ebk_test', $http, $factory, $factory, $org);
    }

    public function testAuthAndOrgHeaders(): void
    {
        $c = $this->makeClient([
            new Response(200, ['Content-Type' => 'application/json'], '{"data":[],"nextCursor":null}'),
        ], 'org_1');
        $c->get('/v1/accounts');
        $req = $this->history[0]['request'];
        $this->assertSame('Bearer ebk_test', $req->getHeaderLine('Authorization'));
        $this->assertSame('org_1', $req->getHeaderLine('EB-Organization'));
    }

    public function testIdempotencyKey(): void
    {
        $c = $this->makeClient([
            new Response(201, ['Content-Type' => 'application/json'], '{"id":"acc_new"}'),
        ]);
        $out = $c->post('/v1/accounts', (object) [], [], [], 'idem-1');
        $this->assertSame('acc_new', $out['id']);
        $this->assertSame('idem-1', $this->history[0]['request']->getHeaderLine('Idempotency-Key'));
    }

    public function testProblemError(): void
    {
        $c = $this->makeClient([
            new Response(422, ['Content-Type' => 'application/problem+json'], json_encode([
                'type' => 'https://emailbridge.dev/problems/validation_failed',
                'title' => 'Validation failed',
                'status' => 422,
                'code' => 'validation_failed',
                'kind' => 'user',
                'detail' => 'bad',
            ])),
        ]);
        $this->expectException(ProblemException::class);
        $c->get('/v1/boom');
    }

    public function testRetry429(): void
    {
        $c = $this->makeClient([
            new Response(429, ['Retry-After' => '0', 'Content-Type' => 'application/problem+json'], '{"type":"t","title":"t","status":429,"code":"rate_limited","kind":"rate_limited"}'),
            new Response(429, ['Retry-After' => '0', 'Content-Type' => 'application/problem+json'], '{"type":"t","title":"t","status":429,"code":"rate_limited","kind":"rate_limited"}'),
            new Response(200, ['Content-Type' => 'application/json'], '{"ok":true}'),
        ]);
        $out = $c->get('/v1/slow');
        $this->assertTrue($out['ok']);
    }

    public function testIterateCursor(): void
    {
        $c = $this->makeClient([
            new Response(200, ['Content-Type' => 'application/json'], '{"data":[{"id":"acc_1"}],"nextCursor":"c2"}'),
            new Response(200, ['Content-Type' => 'application/json'], '{"data":[{"id":"acc_2"}],"nextCursor":null}'),
        ]);
        $ids = [];
        foreach ($c->iterateCursor('/v1/accounts') as $row) {
            $ids[] = $row['id'];
        }
        $this->assertSame(['acc_1', 'acc_2'], $ids);
    }
}
