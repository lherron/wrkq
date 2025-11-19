#!/usr/bin/env bash
# install.sh - Install wrkq CLI
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/lherron/wrkq/main/install.sh | bash
#   ./install.sh
#   ./install.sh --prefix=/usr/local

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
INSTALL_DIR="${HOME}/.local/bin"
COMPLETION_DIR="${HOME}/.local/share/bash-completion/completions"
ZSH_COMPLETION_DIR="${HOME}/.local/share/zsh/site-functions"
FISH_COMPLETION_DIR="${HOME}/.config/fish/completions"

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --prefix)
            INSTALL_DIR="$2/bin"
            shift 2
            ;;
        --prefix=*)
            INSTALL_DIR="${1#*=}/bin"
            shift
            ;;
        --help)
            echo "Usage: $0 [--prefix=DIR]"
            echo ""
            echo "Options:"
            echo "  --prefix=DIR    Install to DIR/bin (default: ~/.local/bin)"
            echo "  --help          Show this help message"
            exit 0
            ;;
        *)
            echo -e "${RED}Error: Unknown option: $1${NC}"
            exit 1
            ;;
    esac
done

# Detect OS and Architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
    linux*)  OS="linux" ;;
    darwin*) OS="darwin" ;;
    *)
        echo -e "${RED}Error: Unsupported OS: $OS${NC}"
        exit 1
        ;;
esac

case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    amd64)   ARCH="amd64" ;;
    arm64)   ARCH="arm64" ;;
    aarch64) ARCH="arm64" ;;
    *)
        echo -e "${RED}Error: Unsupported architecture: $ARCH${NC}"
        exit 1
        ;;
esac

echo "Installing wrkq for $OS/$ARCH"

# Check if binary already exists in repo
BINARY_PATH="./bin/wrkq"
if [ ! -f "$BINARY_PATH" ]; then
    # Try to build from source if Go is available
    if command -v go &> /dev/null; then
        echo -e "${YELLOW}Binary not found, building from source...${NC}"
        go build -o "$BINARY_PATH" ./cmd/wrkq
    else
        echo -e "${RED}Error: Binary not found and Go is not installed${NC}"
        echo "Please build the binary first with 'go build -o bin/wrkq ./cmd/wrkq'"
        exit 1
    fi
fi

# Create installation directory if it doesn't exist
mkdir -p "$INSTALL_DIR"

# Copy binary
echo "Installing wrkq to $INSTALL_DIR/wrkq"
cp "$BINARY_PATH" "$INSTALL_DIR/wrkq"
chmod +x "$INSTALL_DIR/wrkq"

echo -e "${GREEN}✓ Installed wrkq binary${NC}"

# Install shell completions
if [ -x "$INSTALL_DIR/wrkq" ]; then
    # Bash completions
    if [ -n "$BASH_VERSION" ] || command -v bash &> /dev/null; then
        mkdir -p "$COMPLETION_DIR"
        "$INSTALL_DIR/wrkq" completion bash > "$COMPLETION_DIR/wrkq" 2>/dev/null || true
        echo -e "${GREEN}✓ Installed bash completions${NC}"
        echo -e "${YELLOW}  Add this to your ~/.bashrc:${NC}"
        echo -e "    source $COMPLETION_DIR/wrkq"
    fi

    # Zsh completions
    if [ -n "$ZSH_VERSION" ] || command -v zsh &> /dev/null; then
        mkdir -p "$ZSH_COMPLETION_DIR"
        "$INSTALL_DIR/wrkq" completion zsh > "$ZSH_COMPLETION_DIR/_wrkq" 2>/dev/null || true
        echo -e "${GREEN}✓ Installed zsh completions${NC}"
        echo -e "${YELLOW}  Add this to your ~/.zshrc:${NC}"
        echo -e "    fpath=($ZSH_COMPLETION_DIR \$fpath)"
        echo -e "    autoload -Uz compinit && compinit"
    fi

    # Fish completions
    if command -v fish &> /dev/null; then
        mkdir -p "$FISH_COMPLETION_DIR"
        "$INSTALL_DIR/wrkq" completion fish > "$FISH_COMPLETION_DIR/wrkq.fish" 2>/dev/null || true
        echo -e "${GREEN}✓ Installed fish completions${NC}"
    fi
fi

# Check if install directory is in PATH
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo ""
    echo -e "${YELLOW}Warning: $INSTALL_DIR is not in your PATH${NC}"
    echo "Add this to your shell profile:"
    echo "  export PATH=\"\$PATH:$INSTALL_DIR\""
fi

echo ""
echo -e "${GREEN}✓ Installation complete!${NC}"
echo ""
echo "Run 'wrkq --version' to verify the installation"
echo "Run 'wrkq init' to set up your first database"
