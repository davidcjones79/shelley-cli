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

# Create config (auto-detected by binary)
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

# Symlink to /usr/local/bin
echo "🔗 Installing shelley command..."
sudo ln -sf ~/shelley-cli/bin/shelley /usr/local/bin/shelley

# Install Igor service
if [ -d "/exe.dev" ]; then
    echo "⚡ Installing Igor file transfer service..."
    sudo cp ~/shelley-cli/igor.service /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable igor
    sudo systemctl start igor
    
    HOSTNAME=$(hostname)
    echo ""
    echo "✅ Shelley CLI installed!"
    echo ""
    echo "   Run:  shelley chat"
    echo ""
    echo "   Igor: https://${HOSTNAME}.exe.xyz:8001/"
else
    echo ""
    echo "✅ Shelley CLI installed!"
    echo ""
    echo "   Run:  shelley chat"
fi
