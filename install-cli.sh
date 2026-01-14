#!/bin/bash
#
# Shelley CLI installer for exe.dev VMs
#
set -e

# Ensure standard paths are available
export PATH="/usr/local/bin:/usr/bin:/bin:$PATH"

echo "🐡 Installing Shelley CLI..."

# Check if we're on exe.dev
if [ ! -d "/exe.dev" ]; then
    echo "⚠️  Warning: This script is designed for exe.dev VMs."
    echo "   For other environments, see: https://github.com/davidcjones79/shelley-cli/blob/shelley-cli-test/cli/README.md"
    read -p "   Continue anyway? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

# Install Node.js if not present
if ! command -v node &> /dev/null; then
    echo "📦 Installing Node.js..."
    curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
    sudo apt-get install -y nodejs
fi

# Clone or update repo
if [ -d "$HOME/shelley-cli" ]; then
    echo "📦 Updating existing shelley-cli..."
    cd ~/shelley-cli
    git fetch origin
    git checkout main
    git pull origin main
else
    echo "📦 Cloning shelley-cli..."
    git clone https://github.com/davidcjones79/shelley-cli.git ~/shelley-cli
    cd ~/shelley-cli
fi

# Build
echo "🔨 Building (this may take a minute)..."
make

# Create config
echo "⚙️  Setting up config..."
mkdir -p ~/.config/shelley
if [ ! -f ~/.config/shelley/shelley.json ]; then
    cat > ~/.config/shelley/shelley.json << 'EOF'
{
  "llm_gateway": "http://169.254.169.254/gateway/llm",
  "default_model": "claude-sonnet-4.5"
}
EOF
fi

# Create wrapper script in ~/.local/bin (takes precedence over /usr/local/bin)
mkdir -p ~/.local/bin
echo "🔗 Installing shelley command..."
cat > ~/.local/bin/shelley << EOF
#!/bin/bash
exec $HOME/shelley-cli/bin/shelley --config $HOME/.config/shelley/shelley.json "\$@"
EOF
chmod +x ~/.local/bin/shelley

# Ensure ~/.local/bin is in PATH
if ! grep -q 'export PATH="\$HOME/.local/bin:\$PATH"' ~/.bashrc 2>/dev/null; then
    echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
fi
export PATH="$HOME/.local/bin:$PATH"

# Install uploader service
if [ -d "/exe.dev" ]; then
    echo "🚀 Installing file uploader service..."
    sudo cp ~/shelley-cli/shelley-uploader.service /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable shelley-uploader
    sudo systemctl start shelley-uploader
    
    HOSTNAME=$(hostname)
    echo ""
    echo "✅ Shelley CLI installed!"
    echo ""
    echo "   Run:  shelley chat"
    echo ""
    echo "   File uploader: https://${HOSTNAME}.exe.xyz:8099/"
else
    echo ""
    echo "✅ Shelley CLI installed!"
    echo ""
    echo "   Run:  shelley chat"
fi
