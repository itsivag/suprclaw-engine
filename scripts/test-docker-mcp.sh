#!/bin/bash
# Test script for MCP tools in Docker (full-featured image)

set -e

COMPOSE_FILE="docker-compose.full.yml"

echo "🧪 Testing MCP tools in Docker container (full-featured image)..."
echo ""

# Build the image
echo "📦 Building Docker image..."
docker-compose -f "$COMPOSE_FILE" build

# Test npx
echo "✅ Testing npx..."
docker-compose -f "$COMPOSE_FILE" run --rm picoclaw-agent sh -c 'npx --version'

# Test npm
echo "✅ Testing npm..."
docker-compose -f "$COMPOSE_FILE" run --rm picoclaw-agent sh -c 'npm --version'

# Test node
echo "✅ Testing Node.js..."
docker-compose -f "$COMPOSE_FILE" run --rm picoclaw-agent sh -c 'node --version'

# Test git
echo "✅ Testing git..."
docker-compose -f "$COMPOSE_FILE" run --rm picoclaw-agent sh -c 'git --version'

# Test python
echo "✅ Testing Python..."
docker-compose -f "$COMPOSE_FILE" run --rm picoclaw-agent sh -c 'python3 --version'

# Test uv
echo "✅ Testing uv..."
docker-compose -f "$COMPOSE_FILE" run --rm picoclaw-agent sh -c 'uv --version'

# Test MCP server installation (quick)
echo "✅ Testing MCP server install with npx..."
docker-compose -f "$COMPOSE_FILE" run --rm picoclaw-agent sh -c 'npx -y cowsay "MCP works!"'

echo ""
echo "🎉 All MCP tools are working correctly!"
echo ""
echo "Next steps:"
echo "  1. Configure MCP servers in config/config.json"
echo "  2. Run: docker-compose -f $COMPOSE_FILE --profile gateway up"
