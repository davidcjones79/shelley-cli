# Language-Specific Vulnerability Patterns

## Python

### Dangerous Functions
```python
# Command Execution
os.system(user_input)        # Command injection
os.popen(user_input)         # Command injection  
subprocess.call(user_input, shell=True)  # Command injection
eval(user_input)             # Code execution
exec(user_input)             # Code execution
compile(user_input, ...)     # Code execution

# Deserialization
pickle.loads(user_input)     # Arbitrary code execution
yaml.load(user_input)        # Use yaml.safe_load()
marshall.loads(user_input)   # Arbitrary code execution

# SQL
cursor.execute(f"SELECT * FROM users WHERE id = {user_id}")  # SQL injection

# Path Traversal
open(user_input)             # Path traversal
os.path.join(base, user_input)  # Still vulnerable if user_input is absolute!
```

### Safe Alternatives
```python
# Command execution
subprocess.run(['cmd', 'arg1', 'arg2'], shell=False)  # Safe

# Deserialization
json.loads(user_input)       # Safe
yaml.safe_load(user_input)   # Safe

# SQL
cursor.execute("SELECT * FROM users WHERE id = %s", (user_id,))  # Safe

# Path traversal
import os
base = '/safe/path'
full_path = os.path.normpath(os.path.join(base, user_input))
if not full_path.startswith(base):
    raise ValueError("Path traversal detected")
```

### Framework-Specific (Django)
```python
# BAD: XSS
from django.utils.safestring import mark_safe
mark_safe(user_input)        # Never mark user input as safe!

# BAD: SQL injection in extra()
User.objects.extra(where=[f"name = '{name}'"])

# BAD: RawSQL
from django.db.models import RawSQL
User.objects.annotate(val=RawSQL(user_input, ()))
```

### Framework-Specific (Flask)
```python
# BAD: XSS
from flask import Markup
Markup(user_input)           # Dangerous!

# BAD: Debug in production
app.run(debug=True)          # Never in production

# BAD: Secret key
app.secret_key = 'dev'       # Hardcoded secret
```

---

## JavaScript / Node.js

### Dangerous Functions
```javascript
// Command Execution
require('child_process').exec(userInput);  // Command injection
require('child_process').spawn('sh', ['-c', userInput]);  // Command injection
eval(userInput);             // Code execution
new Function(userInput)();   // Code execution
setTimeout(userInput, 1000); // If string, evaluates code

// SQL (raw queries)
db.query(`SELECT * FROM users WHERE id = ${userId}`);  // SQL injection

// Path Traversal
fs.readFile(userInput);      // Path traversal
path.join(base, userInput);  // Can still escape with ../ or absolute paths

// Deserialization
JSON.parse(userInput);       // Safe for data, but...
vm.runInContext(userInput);  // Code execution
```

### Safe Alternatives
```javascript
// Command execution
const { execFile } = require('child_process');
execFile('/usr/bin/program', [arg1, arg2]);  // Safe

// SQL (parameterized)
db.query('SELECT * FROM users WHERE id = ?', [userId]);

// Path traversal
const safePath = path.normalize(path.join(base, userInput));
if (!safePath.startsWith(path.normalize(base))) {
  throw new Error('Path traversal detected');
}
```

### XSS Patterns
```javascript
// BAD: DOM XSS
element.innerHTML = userInput;           // XSS
document.write(userInput);               // XSS
$(selector).html(userInput);             // XSS
React: dangerouslySetInnerHTML={{ __html: userInput }};  // XSS
Vue: v-html="userInput"                  // XSS
Angular: [innerHTML]="userInput"         // XSS (without sanitizer)

// GOOD: Safe rendering
element.textContent = userInput;         // Safe
$(selector).text(userInput);             // Safe
React: {userInput}                       // Auto-escaped
Vue: {{ userInput }}                     // Auto-escaped
```

### Express.js Specific
```javascript
// BAD: Missing security headers
app.use(express.static('public'));  // No security headers

// GOOD: Use helmet
const helmet = require('helmet');
app.use(helmet());

// BAD: CORS wildcard
app.use(cors({ origin: '*' }));  // Too permissive

// GOOD: Specific origins
app.use(cors({ origin: ['https://example.com'] }));
```

---

## Go

### Dangerous Patterns
```go
// Command Execution
exec.Command("sh", "-c", userInput).Run()  // Command injection

// SQL Injection
db.Query(fmt.Sprintf("SELECT * FROM users WHERE id = %s", userID))  // SQLi

// XSS
fmt.Fprintf(w, "<h1>%s</h1>", userInput)  // XSS

// SSRF
resp, _ := http.Get(userURL)  // SSRF

// Path Traversal
http.ServeFile(w, r, filepath.Join("static", userPath))  // Path traversal
```

### Safe Alternatives
```go
// Command execution - use argument array
cmd := exec.Command("program", arg1, arg2)  // Safe

// SQL - use parameterized queries
db.Query("SELECT * FROM users WHERE id = $1", userID)  // Safe

// XSS - use html/template
tmpl.Execute(w, data)  // Auto-escapes

// Path traversal
cleanPath := filepath.Clean(userPath)
if strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
    http.Error(w, "Invalid path", 400)
    return
}
```

### Crypto Patterns
```go
// BAD: Weak random
import "math/rand"
token := rand.Int()  // Predictable!

// GOOD: Crypto random
import "crypto/rand"
buf := make([]byte, 32)
crypto_rand.Read(buf)

// BAD: Weak hashing
import "crypto/md5"
hash := md5.Sum(password)  // Don't use for passwords!

// GOOD: Use bcrypt
import "golang.org/x/crypto/bcrypt"
hash, _ := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
```

---

## Java

### Dangerous Patterns
```java
// Deserialization (extremely dangerous)
ObjectInputStream ois = new ObjectInputStream(userInputStream);
Object obj = ois.readObject();  // Arbitrary code execution!

// SQL Injection
String query = "SELECT * FROM users WHERE id = " + userId;
statement.executeQuery(query);  // SQLi

// Command Injection
Runtime.getRuntime().exec(userCommand);  // Command injection

// XXE (XML External Entity)
DocumentBuilderFactory dbf = DocumentBuilderFactory.newInstance();
// Without disabling external entities - XXE vulnerable

// Path Traversal
new File(basePath + userPath);  // Path traversal
```

### Safe Alternatives
```java
// SQL - PreparedStatement
PreparedStatement ps = conn.prepareStatement("SELECT * FROM users WHERE id = ?");
ps.setString(1, userId);  // Safe

// XXE Prevention
DocumentBuilderFactory dbf = DocumentBuilderFactory.newInstance();
dbf.setFeature("http://apache.org/xml/features/disallow-doctype-decl", true);
dbf.setFeature("http://xml.org/sax/features/external-general-entities", false);
dbf.setFeature("http://xml.org/sax/features/external-parameter-entities", false);

// Path traversal
Path basePath = Paths.get("/safe/base").toAbsolutePath().normalize();
Path fullPath = basePath.resolve(userPath).toAbsolutePath().normalize();
if (!fullPath.startsWith(basePath)) {
    throw new SecurityException("Path traversal detected");
}
```

### Spring-Specific
```java
// BAD: SpEL injection
ExpressionParser parser = new SpelExpressionParser();
parser.parseExpression(userInput).getValue();  // Code execution!

// BAD: Mass assignment
@PostMapping("/user")
public void createUser(User user) { }  // Can set isAdmin=true!

// GOOD: Use DTOs or @JsonIgnore
public class UserDTO {
    private String name;
    private String email;
    // No isAdmin field
}
```

---

## Ruby

### Dangerous Patterns
```ruby
# Command execution
system(user_input)           # Command injection
`#{user_input}`              # Command injection
exec(user_input)             # Command injection
eval(user_input)             # Code execution

# Deserialization
Marshal.load(user_input)     # Code execution
YAML.load(user_input)        # Code execution (use safe_load)

# SQL injection
User.where("name = '#{name}'")  # SQLi
User.find_by_sql(user_input)    # SQLi
```

### Safe Alternatives
```ruby
# Command execution
system('command', arg1, arg2)  # Safe (no shell)

# Deserialization
JSON.parse(user_input)         # Safe
YAML.safe_load(user_input)     # Safe

# SQL
User.where(name: name)         # Safe
User.where("name = ?", name)   # Safe
```

### Rails-Specific
```ruby
# BAD: XSS
<%= raw user_input %>          # XSS
<%= user_input.html_safe %>    # XSS

# GOOD: Auto-escaped
<%= user_input %>              # Safe

# BAD: Mass assignment (older Rails)
User.new(params[:user])        # Can set admin=true

# GOOD: Strong parameters
User.new(params.require(:user).permit(:name, :email))

# BAD: Open redirect
redirect_to params[:url]       # Open redirect

# GOOD: Validate redirect
redirect_to params[:url] if params[:url].start_with?('/')
```
