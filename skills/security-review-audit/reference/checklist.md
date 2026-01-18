# Security Review Checklist

Use this checklist when performing security reviews. Check off items as you review them.

## Input Validation

- [ ] All user input is validated on the server side
- [ ] Input validation uses allowlists, not denylists
- [ ] Input length limits are enforced
- [ ] Input type/format is validated (email, URL, numbers)
- [ ] File uploads validate file type, size, and content
- [ ] File names are sanitized before use

## Output Encoding

- [ ] All output is encoded for the appropriate context (HTML, JS, URL, CSS)
- [ ] Content-Type headers are set correctly
- [ ] X-Content-Type-Options: nosniff is set
- [ ] User-controlled data is never used in dangerous sinks without encoding

## Authentication

- [ ] Passwords are hashed with bcrypt, argon2, or scrypt
- [ ] Password policy enforces minimum complexity
- [ ] Account lockout after failed attempts
- [ ] Multi-factor authentication available for sensitive accounts
- [ ] Secure password reset process
- [ ] No default credentials
- [ ] No hardcoded credentials in code

## Session Management

- [ ] Session IDs are random and high entropy
- [ ] Session IDs are not in URLs
- [ ] Cookies have Secure flag (HTTPS only)
- [ ] Cookies have HttpOnly flag (no JS access)
- [ ] Cookies have SameSite attribute
- [ ] Sessions expire after inactivity
- [ ] Sessions are invalidated on logout
- [ ] Session is regenerated after login

## Authorization

- [ ] Every endpoint checks authorization
- [ ] Principle of least privilege applied
- [ ] Direct object references are validated
- [ ] Function-level access control is enforced
- [ ] Admin functions require admin role
- [ ] Authorization cannot be bypassed by parameter manipulation

## SQL/Database

- [ ] All queries use parameterized statements
- [ ] No string concatenation in queries
- [ ] Database user has minimum required privileges
- [ ] Sensitive data is encrypted at rest
- [ ] Database errors don't leak to users

## Cryptography

- [ ] No weak algorithms (MD5, SHA1 for security, DES, RC4)
- [ ] Strong random number generation (crypto/rand, not math/rand)
- [ ] No hardcoded keys or secrets
- [ ] Secrets loaded from environment/vault
- [ ] TLS 1.2+ required for all connections
- [ ] Certificate validation enabled

## Error Handling

- [ ] Errors don't reveal stack traces in production
- [ ] Errors don't reveal sensitive information
- [ ] All exceptions are caught and handled
- [ ] Error messages are generic for users
- [ ] Detailed errors only in logs

## Logging

- [ ] Authentication events are logged
- [ ] Authorization failures are logged
- [ ] Sensitive data is NOT logged (passwords, tokens, PII)
- [ ] Logs include timestamp, user, action, result
- [ ] Logs are tamper-evident
- [ ] Log injection is prevented

## File Operations

- [ ] Path traversal prevented (../ attacks)
- [ ] Uploaded files stored outside webroot
- [ ] Uploaded files have random names
- [ ] File type validated by content, not extension
- [ ] Maximum file size enforced

## API Security

- [ ] Rate limiting implemented
- [ ] API authentication required
- [ ] CORS configured restrictively
- [ ] Input size limits enforced
- [ ] Sensitive endpoints require re-authentication

## HTTP Security Headers

- [ ] Strict-Transport-Security (HSTS)
- [ ] X-Content-Type-Options: nosniff
- [ ] X-Frame-Options: DENY or SAMEORIGIN
- [ ] Content-Security-Policy
- [ ] X-XSS-Protection: 1; mode=block (legacy browsers)
- [ ] Referrer-Policy: strict-origin-when-cross-origin

## Dependencies

- [ ] No known vulnerable dependencies
- [ ] Dependencies from trusted sources
- [ ] Dependency versions pinned
- [ ] Regular vulnerability scanning
- [ ] Unused dependencies removed

## Secrets Management

- [ ] No secrets in code or version control
- [ ] Secrets in environment variables or vault
- [ ] Different secrets for dev/staging/prod
- [ ] Secrets can be rotated without code changes
- [ ] .env files are in .gitignore

## Deployment

- [ ] Debug mode disabled in production
- [ ] Default accounts disabled/removed
- [ ] Unnecessary services disabled
- [ ] Firewall rules restrict access
- [ ] Container runs as non-root
- [ ] Sensitive config not in container image
