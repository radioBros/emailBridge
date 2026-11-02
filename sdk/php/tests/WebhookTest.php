<?php

declare(strict_types=1);

namespace EmailBridge\Sdk\Tests;

use EmailBridge\Sdk\Webhook;
use EmailBridge\Sdk\WebhookException;
use PHPUnit\Framework\TestCase;

final class WebhookTest extends TestCase
{
    private string $secret;
    private string $body = '{"ok":true}';
    private string $id = 'evt_1';
    private int $ts = 1700000000;

    protected function setUp(): void
    {
        $key = '0123456789abcdef0123456789abcdef';
        $this->secret = 'whsec_' . base64_encode($key);
    }

    public function testSignVerify(): void
    {
        $sig = Webhook::sign($this->secret, $this->id, $this->ts, $this->body);
        Webhook::verify($this->secret, [
            Webhook::HEADER_ID => $this->id,
            Webhook::HEADER_TIMESTAMP => (string) $this->ts,
            Webhook::HEADER_SIGNATURE => $sig,
        ], $this->body, $this->ts);
        $this->assertTrue(true);
    }

    public function testBadSignature(): void
    {
        $this->expectException(WebhookException::class);
        Webhook::verify($this->secret, [
            Webhook::HEADER_ID => $this->id,
            Webhook::HEADER_TIMESTAMP => (string) $this->ts,
            Webhook::HEADER_SIGNATURE => 'v1,aaaa',
        ], $this->body, $this->ts);
    }

    public function testStaleTimestamp(): void
    {
        $sig = Webhook::sign($this->secret, $this->id, $this->ts, $this->body);
        $this->expectException(WebhookException::class);
        Webhook::verify($this->secret, [
            Webhook::HEADER_ID => $this->id,
            Webhook::HEADER_TIMESTAMP => (string) $this->ts,
            Webhook::HEADER_SIGNATURE => $sig,
        ], $this->body, $this->ts + 600);
    }

    public function testBodyOnly(): void
    {
        $raw = 'org-secret';
        $sig = Webhook::signBodyOnly($raw, $this->body);
        Webhook::verifyBodyOnly($raw, [Webhook::HEADER_EB_SIGNATURE => $sig], $this->body);
        $this->assertTrue(true);
    }

    public function testDualSignatures(): void
    {
        $sig = Webhook::sign($this->secret, $this->id, $this->ts, $this->body);
        Webhook::verify($this->secret, [
            Webhook::HEADER_ID => $this->id,
            Webhook::HEADER_TIMESTAMP => (string) $this->ts,
            Webhook::HEADER_SIGNATURE => "v1,deadbeef {$sig}",
        ], $this->body, $this->ts);
        $this->assertTrue(true);
    }
}
