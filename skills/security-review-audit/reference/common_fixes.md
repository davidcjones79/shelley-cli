# Common Security Fixes

Code examples for fixing common vulnerabilities.

## SQL Injection Fix

### Before (Vulnerable)
```python
# Python
query = f"SELECT * FROM users WHERE email = '{email}'"
cursor.execute(query)
```

```javascript
// JavaScript
const query = `SELECT * FROM users WHERE email = '${email}'`;
db.query(query);
```

```go
// Go
query := fmt.Sprintf("SELECT * FROM users WHERE email = '%s'", email)
db.Query(query)
```

### After (Secure)
```python
# Python
cursor.execute("SELECT * FROM users WHERE email = %s", (email,))
```

```javascript
// JavaScript
db.query('SELECT * FROM users WHERE email = ?', [email]);
```

```go
// Go
db.Query("SELECT * FROM users WHERE email = $1", email)
```

---

## XSS Fix

### Before (Vulnerable)
```javascript
// DOM XSS
document.getElementById('output').innerHTML = userInput;
```

```python
# Flask
@app.route('/greet')
def greet():
    name = request.args.get('name')
    return f'<h1>Hello {name}</h1>'
```

### After (Secure)
```javascript
// Use textContent
document.getElementById('output').textContent = userInput;

// Or sanitize with DOMPurify
document.getElementById('output').innerHTML = DOMPurify.sanitize(userInput);
```

```python
# Flask - use templates (auto-escape)
from flask import render_template

@app.route('/greet')
def greet():
    name = request.args.get('name')
    return render_template('greet.html', name=name)

# greet.html: <h1>Hello {{ name }}</h1>
```

---

## Command Injection Fix

### Before (Vulnerable)
```python
import os
os.system(f"convert {filename} output.png")
```

```javascript
const { exec } = require('child_process');
exec(`convert ${filename} output.png`);
```

### After (Secure)
```python
import subprocess
# Use array form - no shell
subprocess.run(['convert', filename, 'output.png'], check=True)
```

```javascript
const { execFile } = require('child_process');
// Use execFile with array - no shell
execFile('convert', [filename, 'output.png']);
```

---

## Path Traversal Fix

### Before (Vulnerable)
```python
@app.route('/files/<path:filename>')
def get_file(filename):
    return send_file(os.path.join('uploads', filename))
```

### After (Secure)
```python
import os
from flask import abort

UPLOAD_DIR = os.path.abspath('uploads')

@app.route('/files/<path:filename>')
def get_file(filename):
    # Normalize and check the path
    safe_path = os.path.normpath(os.path.join(UPLOAD_DIR, filename))
    
    # Ensure it's still under our upload directory
    if not safe_path.startswith(UPLOAD_DIR + os.sep):
        abort(403)  # Path traversal attempt
    
    if not os.path.isfile(safe_path):
        abort(404)
    
    return send_file(safe_path)
```

---

## Insecure Password Storage Fix

### Before (Vulnerable)
```python
import hashlib
password_hash = hashlib.sha256(password.encode()).hexdigest()
```

### After (Secure)
```python
import bcrypt

# Hash password
def hash_password(password: str) -> bytes:
    return bcrypt.hashpw(password.encode(), bcrypt.gensalt())

# Verify password
def verify_password(password: str, hashed: bytes) -> bool:
    return bcrypt.checkpw(password.encode(), hashed)
```

```javascript
const bcrypt = require('bcrypt');

// Hash password
const hash = await bcrypt.hash(password, 12);

// Verify password
const match = await bcrypt.compare(password, hash);
```

---

## Hardcoded Secrets Fix

### Before (Vulnerable)
```python
JWT_SECRET = "mysupersecretkey123"
DB_PASSWORD = "admin123"
```

### After (Secure)
```python
import os

JWT_SECRET = os.environ['JWT_SECRET']
DB_PASSWORD = os.environ['DB_PASSWORD']

# Or with defaults for dev (but require in prod)
JWT_SECRET = os.environ.get('JWT_SECRET')
if not JWT_SECRET:
    raise RuntimeError("JWT_SECRET environment variable required")
```

```bash
# .env file (add to .gitignore!)
JWT_SECRET=your-secure-random-secret-here
DB_PASSWORD=your-secure-password-here
```

---

## IDOR (Insecure Direct Object Reference) Fix

### Before (Vulnerable)
```python
@app.route('/api/orders/<order_id>')
def get_order(order_id):
    order = Order.query.get(order_id)
    return jsonify(order.to_dict())
```

### After (Secure)
```python
from flask_login import current_user, login_required

@app.route('/api/orders/<order_id>')
@login_required
def get_order(order_id):
    order = Order.query.get_or_404(order_id)
    
    # Check ownership
    if order.user_id != current_user.id:
        abort(403)  # Forbidden
    
    return jsonify(order.to_dict())
```

---

## SSRF Fix

### Before (Vulnerable)
```python
import requests

@app.route('/fetch')
def fetch_url():
    url = request.args.get('url')
    return requests.get(url).text
```

### After (Secure)
```python
import requests
from urllib.parse import urlparse
import ipaddress
import socket

ALLOWED_HOSTS = {'api.example.com', 'cdn.example.com'}

def is_safe_url(url):
    try:
        parsed = urlparse(url)
        
        # Only allow http/https
        if parsed.scheme not in ('http', 'https'):
            return False
        
        # Check against allowlist
        if parsed.hostname not in ALLOWED_HOSTS:
            return False
        
        # Resolve and check for internal IPs
        ip = socket.gethostbyname(parsed.hostname)
        ip_obj = ipaddress.ip_address(ip)
        if ip_obj.is_private or ip_obj.is_loopback:
            return False
        
        return True
    except:
        return False

@app.route('/fetch')
def fetch_url():
    url = request.args.get('url')
    
    if not is_safe_url(url):
        abort(400, "Invalid or disallowed URL")
    
    return requests.get(url, timeout=5).text
```

---

## Insecure Cookie Fix

### Before (Vulnerable)
```python
response.set_cookie('session', session_id)
```

```javascript
res.cookie('session', sessionId);
```

### After (Secure)
```python
response.set_cookie(
    'session',
    session_id,
    httponly=True,    # No JavaScript access
    secure=True,      # HTTPS only
    samesite='Lax',   # CSRF protection
    max_age=3600      # 1 hour expiry
)
```

```javascript
res.cookie('session', sessionId, {
    httpOnly: true,
    secure: true,
    sameSite: 'lax',
    maxAge: 3600000
});
```

---

## Missing Rate Limiting Fix

### Before (Vulnerable)
```python
@app.route('/login', methods=['POST'])
def login():
    # No rate limiting - brute force possible
    if check_password(request.form['password']):
        return redirect('/dashboard')
    return 'Invalid password', 401
```

### After (Secure)
```python
from flask_limiter import Limiter
from flask_limiter.util import get_remote_address

limiter = Limiter(
    app,
    key_func=get_remote_address,
    default_limits=["200 per day", "50 per hour"]
)

@app.route('/login', methods=['POST'])
@limiter.limit("5 per minute")  # Strict limit on login
def login():
    if check_password(request.form['password']):
        return redirect('/dashboard')
    return 'Invalid password', 401
```

---

## Security Headers Fix

```python
# Flask
@app.after_request
def add_security_headers(response):
    response.headers['Strict-Transport-Security'] = 'max-age=31536000; includeSubDomains'
    response.headers['X-Content-Type-Options'] = 'nosniff'
    response.headers['X-Frame-Options'] = 'DENY'
    response.headers['X-XSS-Protection'] = '1; mode=block'
    response.headers['Content-Security-Policy'] = "default-src 'self'"
    response.headers['Referrer-Policy'] = 'strict-origin-when-cross-origin'
    return response
```

```javascript
// Express - use helmet
const helmet = require('helmet');
app.use(helmet());
```

```go
// Go
func securityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("Content-Security-Policy", "default-src 'self'")
        next.ServeHTTP(w, r)
    })
}
```
