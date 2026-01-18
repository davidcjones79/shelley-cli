# OWASP Top 10 (2021) - Detailed Reference

## A01:2021 - Broken Access Control

### Description
Access control enforces policy such that users cannot act outside of their intended permissions.

### Common Vulnerabilities
- Bypassing access control checks by modifying URL, app state, or HTML page
- Allowing primary key to be changed to another user's record (IDOR)
- Elevation of privilege (acting as user without login, or as admin when logged in as user)
- Metadata manipulation (replaying/tampering JWT, cookie, hidden field)
- CORS misconfiguration allowing unauthorized API access
- Force browsing to authenticated pages or privileged pages

### Code Patterns to Find

```python
# BAD: IDOR - user can access any order
@app.route('/order/<order_id>')
def get_order(order_id):
    return Order.query.get(order_id)  # No ownership check!

# GOOD: Verify ownership
@app.route('/order/<order_id>')
@login_required
def get_order(order_id):
    order = Order.query.get(order_id)
    if order.user_id != current_user.id:
        abort(403)
    return order
```

```javascript
// BAD: Missing authorization check
app.delete('/api/users/:id', async (req, res) => {
  await User.deleteOne({ _id: req.params.id });
});

// GOOD: Check permissions
app.delete('/api/users/:id', requireAdmin, async (req, res) => {
  await User.deleteOne({ _id: req.params.id });
});
```

### Remediation
- Deny by default, except for public resources
- Implement access control mechanisms once and reuse throughout the application
- Model access control should enforce record ownership
- Disable web server directory listing and ensure metadata files are not present
- Log access control failures, alert admins on repeated failures
- Rate limit API and controller access
- Invalidate stateful session identifiers on logout

---

## A02:2021 - Cryptographic Failures

### Description
Failures related to cryptography which often lead to sensitive data exposure.

### Common Vulnerabilities
- Transmitting data in clear text (HTTP, SMTP, FTP)
- Using old or weak cryptographic algorithms (MD5, SHA1, DES)
- Using default crypto keys or weak keys
- Not enforcing encryption (missing TLS directives)
- Not validating server certificates
- Using deprecated hash functions for passwords

### Code Patterns to Find

```python
# BAD: Weak password hashing
import hashlib
password_hash = hashlib.md5(password.encode()).hexdigest()

# GOOD: Use bcrypt or argon2
import bcrypt
password_hash = bcrypt.hashpw(password.encode(), bcrypt.gensalt())
```

```go
// BAD: Hardcoded secret
var jwtSecret = []byte("mysecretkey123")

// GOOD: Load from environment
var jwtSecret = []byte(os.Getenv("JWT_SECRET"))
```

```javascript
// BAD: Disabled certificate validation
const https = require('https');
const agent = new https.Agent({ rejectUnauthorized: false });

// GOOD: Validate certificates (default behavior)
const agent = new https.Agent(); // rejectUnauthorized: true by default
```

### Remediation
- Classify data and apply controls according to classification
- Don't store sensitive data unnecessarily
- Encrypt all sensitive data at rest and in transit
- Use strong adaptive hashing for passwords (Argon2, bcrypt, scrypt)
- Ensure up-to-date and strong algorithms and key management
- Disable caching for responses containing sensitive data

---

## A03:2021 - Injection

### Description
User-supplied data is not validated, filtered, or sanitized by the application.

### Common Vulnerabilities
- SQL Injection
- NoSQL Injection
- OS Command Injection
- LDAP Injection
- Expression Language Injection
- XSS (Cross-Site Scripting)

### Code Patterns to Find

```python
# BAD: SQL Injection
query = f"SELECT * FROM users WHERE username = '{username}'"
cursor.execute(query)

# GOOD: Parameterized query
cursor.execute("SELECT * FROM users WHERE username = %s", (username,))
```

```javascript
// BAD: Command Injection
const { exec } = require('child_process');
exec(`convert ${userInput}.png output.jpg`);  // userInput could be "; rm -rf /"

// GOOD: Use array form
const { execFile } = require('child_process');
execFile('convert', [`${sanitizedInput}.png`, 'output.jpg']);
```

```go
// BAD: XSS via template
fmt.Fprintf(w, "<h1>Hello, %s</h1>", userInput)

// GOOD: Use html/template
tmpl.Execute(w, userInput)  // Auto-escapes
```

### Remediation
- Use safe APIs with parameterized interfaces
- Use positive server-side input validation
- Escape special characters for the specific interpreter
- Use LIMIT and other SQL controls within queries to prevent mass disclosure
- For XSS, use context-aware output encoding

---

## A04:2021 - Insecure Design

### Description
Missing or ineffective control design. Not about implementation bugs but fundamentally insecure design.

### Common Vulnerabilities
- Missing rate limiting on sensitive operations
- No account lockout after failed login attempts
- Password reset tokens that don't expire
- Security questions with guessable answers
- Missing re-authentication for sensitive operations

### Questions to Ask
- Is there a threat model for this feature?
- Are trust boundaries clearly defined?
- Is the principle of least privilege applied?
- Are there defense-in-depth measures?
- Is there separation of duties where needed?

### Remediation
- Establish and use a secure development lifecycle
- Establish and use a library of secure design patterns
- Use threat modeling for critical authentication and access control
- Integrate security language and controls into user stories
- Write unit and integration tests to validate critical flows

---

## A05:2021 - Security Misconfiguration

### Description
Missing appropriate security hardening or improperly configured permissions.

### Common Vulnerabilities
- Unnecessary features enabled (ports, services, pages, accounts)
- Default accounts and passwords unchanged
- Error handling reveals stack traces
- Missing security headers
- Software out of date or vulnerable

### What to Check

```bash
# Check security headers
curl -I https://example.com

# Should see:
# Strict-Transport-Security: max-age=31536000; includeSubDomains
# X-Content-Type-Options: nosniff
# X-Frame-Options: DENY
# Content-Security-Policy: default-src 'self'
# X-XSS-Protection: 1; mode=block
```

```python
# BAD: Debug mode in production
app = Flask(__name__)
app.config['DEBUG'] = True  # Exposes debugger!

# GOOD: Disable in production
app.config['DEBUG'] = os.environ.get('FLASK_DEBUG', 'false') == 'true'
```

### Remediation
- Repeatable hardening process for fast, secure deployment
- Minimal platform without unnecessary features
- Review and update configurations as part of patch management
- Segmented application architecture
- Automated verification of configurations

---

## A06:2021 - Vulnerable and Outdated Components

### Description
Using components with known vulnerabilities.

### How to Check

```bash
# Python
pip-audit
safety check

# JavaScript
npm audit
yarn audit

# Go
govulncheck ./...

# General
trivy fs .
snyk test
```

### Remediation
- Remove unused dependencies, features, components
- Continuously inventory versions of components
- Only obtain components from official sources
- Monitor for unmaintained libraries
- Have a patch management process

---

## A07:2021 - Identification and Authentication Failures

### Description
Confirmation of user's identity, authentication, and session management weaknesses.

### Common Vulnerabilities
- Permits automated attacks (credential stuffing, brute force)
- Permits default, weak, or well-known passwords
- Uses weak credential recovery processes
- Uses plain text, encrypted, or weakly hashed passwords
- Missing or ineffective multi-factor authentication
- Exposes session identifier in URL
- Does not properly invalidate session IDs

### Code Patterns to Find

```python
# BAD: Timing attack on password comparison
if stored_password == provided_password:
    return True

# GOOD: Constant-time comparison
import hmac
if hmac.compare_digest(stored_password, provided_password):
    return True
```

```javascript
// BAD: Weak session configuration
app.use(session({
  secret: 'keyboard cat',
  cookie: { secure: false }
}));

// GOOD: Secure session configuration
app.use(session({
  secret: process.env.SESSION_SECRET,
  cookie: {
    secure: true,
    httpOnly: true,
    sameSite: 'strict',
    maxAge: 3600000
  },
  resave: false,
  saveUninitialized: false
}));
```

### Remediation
- Implement multi-factor authentication
- Do not ship with default credentials
- Implement weak password checks against top 10,000 worst passwords
- Ensure registration and credential recovery use same messages
- Limit or increasingly delay failed login attempts
- Use server-side, secure session manager with high entropy

---

## A08:2021 - Software and Data Integrity Failures

### Description
Code and infrastructure that does not protect against integrity violations.

### Common Vulnerabilities
- Insecure deserialization
- Using components from untrusted sources
- CI/CD pipelines without integrity verification
- Auto-update without integrity verification

### Code Patterns to Find

```python
# BAD: Insecure deserialization
import pickle
data = pickle.loads(user_input)  # Can execute arbitrary code!

# GOOD: Use safe formats
import json
data = json.loads(user_input)
```

```java
// BAD: Java deserialization
ObjectInputStream ois = new ObjectInputStream(userInput);
Object obj = ois.readObject();  // Dangerous!

// GOOD: Use allowlist or safe alternatives
// Use JSON, XML, or protocol buffers instead
```

### Remediation
- Use digital signatures to verify software/data is from expected source
- Ensure libraries are consumed from trusted repositories
- Use software supply chain security tools
- Ensure CI/CD pipeline has proper segregation and access control
- Do not send unsigned or unencrypted serialized data to untrusted clients

---

## A09:2021 - Security Logging and Monitoring Failures

### Description
Insufficient logging, detection, monitoring, and active response.

### What Should Be Logged
- Authentication events (login, logout, failed attempts)
- Access control failures
- Server-side input validation failures
- Transactions with high-value data
- All administrative functions

### What Should NOT Be Logged
- Passwords (even hashed)
- Session tokens
- Credit card numbers
- API keys or secrets
- PII without consent

### Code Patterns to Find

```python
# BAD: Logging sensitive data
logger.info(f"User {user} logged in with password {password}")

# GOOD: Log events without secrets
logger.info(f"User {user} logged in from {ip_address}")
```

### Remediation
- Ensure all login, access control, and server-side validation failures are logged
- Ensure logs are in a format easily consumed by log management solutions
- Ensure high-value transactions have audit trail with integrity controls
- Establish or adopt an incident response and recovery plan

---

## A10:2021 - Server-Side Request Forgery (SSRF)

### Description
Fetching a remote resource without validating the user-supplied URL.

### Common Attack Scenarios
- Access internal services (metadata APIs, internal APIs)
- Scan internal ports
- Access cloud metadata (AWS, GCP, Azure)
- Read local files via file:// protocol

### Code Patterns to Find

```python
# BAD: Unvalidated URL fetch
import requests
def fetch_url(user_url):
    return requests.get(user_url).text  # Can access internal network!

# GOOD: Validate and restrict URLs
from urllib.parse import urlparse

ALLOWED_HOSTS = ['api.example.com', 'cdn.example.com']

def fetch_url(user_url):
    parsed = urlparse(user_url)
    if parsed.scheme not in ['http', 'https']:
        raise ValueError("Invalid scheme")
    if parsed.hostname not in ALLOWED_HOSTS:
        raise ValueError("Host not allowed")
    return requests.get(user_url).text
```

### Remediation
- Segment remote resource access functionality
- Enforce "deny by default" firewall policies
- Sanitize and validate all client-supplied input data
- Do not send raw responses to clients
- Disable HTTP redirections
- Use allowlist for URL schemas, ports, and destinations
