<?php

declare(strict_types=1);

namespace EmailBridge\Sdk;

final class WebhookException extends \RuntimeException
{
}

final class Webhook
{
    public const HEADER_ID = 'webhook-id';
    public const HEADER_TIMESTAMP = 'webhook-timestamp';
    public const HEADER_SIGNATURE = 'webhook-signature';
    public const HEADER_EB_SIGNATURE = 'eb-signature';
    public const DEFAULT_TOLERANCE = 300;

    /** @param array<string, string|list<string>> $headers */
    public static function verify(
        string $secret,
        array $headers,
        string $body,
        ?int $now = null,
        int $tolerance = self::DEFAULT_TOLERANCE,
    ): void {
        $id = self::header($headers, self::HEADER_ID);
        $tsHdr = self::header($headers, self::HEADER_TIMESTAMP);
        $sigHdr = self::header($headers, self::HEADER_SIGNATURE);
        if ($id === '' || $tsHdr === '' || $sigHdr === '') {
            throw new WebhookException('webhook: missing headers');
        }
        if (!ctype_digit($tsHdr) && !preg_match('/^-?\d+$/', $tsHdr)) {
            throw new WebhookException('webhook: timestamp out of tolerance');
        }
        $ts = (int) $tsHdr;
        $now ??= time();
        if (abs($now - $ts) > $tolerance) {
            throw new WebhookException('webhook: timestamp out of tolerance');
        }
        $expected = self::sign($secret, $id, $ts, $body);
        self::matchSignatures($sigHdr, explode(',', $expected, 2)[1]);
    }

    /** @param array<string, string|list<string>> $headers */
    public static function verifyBodyOnly(string $secret, array $headers, string $body): void
    {
        $sigHdr = self::header($headers, self::HEADER_EB_SIGNATURE);
        if ($sigHdr === '') {
            throw new WebhookException('webhook: missing headers');
        }
        $expected = self::signBodyOnly($secret, $body);
        self::matchSignatures($sigHdr, explode(',', $expected, 2)[1]);
    }

    public static function sign(string $secret, string $id, int $ts, string $body): string
    {
        $key = self::decodeSecret($secret);
        $toSign = $id . '.' . $ts . '.' . $body;
        return 'v1,' . base64_encode(hash_hmac('sha256', $toSign, $key, true));
    }

    public static function signBodyOnly(string $secret, string $body): string
    {
        return 'v1,' . base64_encode(hash_hmac('sha256', $body, $secret, true));
    }

    private static function decodeSecret(string $secret): string
    {
        $raw = str_starts_with($secret, 'whsec_') ? substr($secret, 6) : $secret;
        $key = base64_decode($raw, true);
        if ($key === false || $key === '') {
            throw new WebhookException('webhook: invalid secret');
        }
        return $key;
    }

    /** @param array<string, string|list<string>> $headers */
    private static function header(array $headers, string $name): string
    {
        foreach ($headers as $k => $v) {
            if (strcasecmp((string) $k, $name) === 0) {
                return is_array($v) ? (string) ($v[0] ?? '') : (string) $v;
            }
        }
        return '';
    }

    private static function matchSignatures(string $header, string $expectedB64): void
    {
        foreach (preg_split('/\s+/', trim($header)) ?: [] as $part) {
            if ($part === '' || !str_contains($part, ',')) {
                continue;
            }
            [$version, $sig] = explode(',', $part, 2);
            if ($version === 'v1' && hash_equals($expectedB64, $sig)) {
                return;
            }
        }
        throw new WebhookException('webhook: signature mismatch');
    }
}
