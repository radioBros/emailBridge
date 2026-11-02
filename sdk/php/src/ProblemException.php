<?php

declare(strict_types=1);

namespace EmailBridge\Sdk;

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
