#!/bin/bash

# TrendJournal Setup Script
echo "🚀 Setting up TrendJournal bot..."

# Check if config.yaml already exists
if [ -f "config.yaml" ]; then
    echo "⚠️  config.yaml already exists. Backing up to config.yaml.backup"
    cp config.yaml config.yaml.backup
fi

# Copy example config
echo "📋 Creating config.yaml from template..."
cp config.example.yaml config.yaml

echo ""
echo "✅ Configuration file created!"
echo ""
echo "🔧 Next steps:"
echo "1. Edit config.yaml and add your Bluesky credentials:"
echo "   - Set your handle (e.g., 'yourname.bsky.social')"
echo "   - Set your app password (not your regular password)"
echo ""
echo "2. Run the bot:"
echo "   make run"
echo ""
echo "3. Or test in dry-run mode first:"
echo "   make dry-run"
echo ""
echo "🔒 Security note:"
echo "   - config.yaml contains your credentials and is git-ignored"
echo "   - Never commit your actual credentials to git"
echo "   - Use app passwords, not your regular Bluesky password"
echo ""
echo "📖 For more help, see README.md and TESTING.md"
