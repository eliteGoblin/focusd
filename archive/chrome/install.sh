#!/usr/bin/env bash
# Chrome Focus Installation Script
# Works on Ubuntu and macOS

set -e

echo "🔧 Installing Chrome Focus..."
echo

# Detect OS
OS="unknown"
if [[ "$OSTYPE" == "linux-gnu"* ]]; then
    OS="linux"
    echo "📍 Detected: Linux"
elif [[ "$OSTYPE" == "darwin"* ]]; then
    OS="macos"
    echo "📍 Detected: macOS"
else
    echo "❌ Unsupported OS: $OSTYPE"
    exit 1
fi
echo

# Get script directory (where chrome folder is)
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

# Check if Poetry is installed
if ! command -v poetry &> /dev/null && [ ! -f "$HOME/.local/bin/poetry" ]; then
    echo "📦 Poetry not found. Installing..."
    curl -sSL https://install.python-poetry.org | python3 -
    echo "✓ Poetry installed"
    echo
else
    echo "✓ Poetry already installed"
    echo
fi

# Set Poetry path
if [ -f "$HOME/.local/bin/poetry" ]; then
    POETRY="$HOME/.local/bin/poetry"
else
    POETRY="poetry"
fi

# Configure Poetry to use in-project venv
cd "$SCRIPT_DIR"
$POETRY config virtualenvs.in-project true

# Install dependencies
echo "📚 Installing dependencies..."
$POETRY install --no-root
echo "✓ Dependencies installed"
echo

# Install wrapper script to /usr/local/bin (requires sudo)
echo "📋 Installing 'cf' wrapper to /usr/local/bin..."
if sudo -S cp "$SCRIPT_DIR/cf" /usr/local/bin/cf && sudo -S chmod +x /usr/local/bin/cf; then
    echo "✓ Wrapper installed to /usr/local/bin/cf"
    echo
else
    echo "❌ Failed to install wrapper. Make sure you have sudo access."
    exit 1
fi

# Verify /usr/local/bin is in PATH
if [[ ":$PATH:" == *":/usr/local/bin:"* ]]; then
    echo "✓ /usr/local/bin is in PATH"
    echo
fi

# Check Python version
PYTHON_VERSION=$(python3 --version 2>&1 | awk '{print $2}')
echo "🐍 Python version: $PYTHON_VERSION"
echo

echo "✅ Installation complete!"
echo
echo "Usage:"
echo "  cf status          # Check daemon status"
echo "  sudo cf on         # Enable Chrome Focus"
echo "  sudo cf off        # Disable Chrome Focus (requires typing quote)"
echo "  cf --help          # Show all commands"
echo
echo "Next steps:"
echo "1. Run: sudo cf on"
echo "2. Restart Chrome"
echo "3. Check chrome://extensions - plugins will be enforced!"
echo
