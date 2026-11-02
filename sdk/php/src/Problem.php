<?php

declare(strict_types=1);

namespace EmailBridge\Sdk;

final class ProblemItem
{
    public function __construct(
        public readonly string $path,
        public readonly string $code,
        public readonly string $message,
    ) {
    }
}

final class Problem
{
    /** @param list<ProblemItem>|null $errors */
    public function __construct(
        public readonly string $type,
        public readonly string $title,
        public readonly int $status,
        public readonly string $code,
        public readonly string $kind,
        public readonly ?string $detail = null,
        public readonly ?string $instance = null,
        public readonly ?string $traceId = null,
        public readonly ?string $requestId = null,
        public readonly ?array $errors = null,
    ) {
    }

    /** @param mixed $body */
    public static function fromBody(mixed $body, int $status): self
    {
        if (!is_array($body)) {
            return new self(
                type: "https://emailbridge.dev/problems/http_{$status}",
                title: 'Error',
                status: $status,
                code: "http_{$status}",
                kind: 'temporary',
            );
        }
        $errors = null;
        if (isset($body['errors']) && is_array($body['errors'])) {
            $errors = [];
            foreach ($body['errors'] as $e) {
                if (!is_array($e)) {
                    continue;
                }
                $errors[] = new ProblemItem(
                    (string) ($e['path'] ?? ''),
                    (string) ($e['code'] ?? ''),
                    (string) ($e['message'] ?? ''),
                );
            }
        }
        return new self(
            type: (string) ($body['type'] ?? "https://emailbridge.dev/problems/http_{$status}"),
            title: (string) ($body['title'] ?? 'Error'),
            status: (int) ($body['status'] ?? $status),
            code: (string) ($body['code'] ?? "http_{$status}"),
            kind: (string) ($body['kind'] ?? 'temporary'),
            detail: isset($body['detail']) ? (string) $body['detail'] : null,
            instance: isset($body['instance']) ? (string) $body['instance'] : null,
            traceId: isset($body['traceId']) ? (string) $body['traceId'] : null,
            requestId: isset($body['requestId']) ? (string) $body['requestId'] : null,
            errors: $errors,
        );
    }
}

final class ProblemException extends \RuntimeException
{
    /** @param array<string, string> $headers */
    public function __construct(
        public readonly Problem $problem,
        public readonly array $headers = [],
    ) {
        parent::__construct($problem->detail ?? $problem->title ?? $problem->code);
    }
}
