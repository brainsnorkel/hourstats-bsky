#!/usr/bin/env sh
# Renders every Mermaid source in this directory to SVG (committed) and PNG (review only).
# Requires mermaid-cli: npm install -g @mermaid-js/mermaid-cli
set -eu
cd "$(dirname "$0")"
for f in *.mmd; do
  base="${f%.mmd}"
  mmdc -q -i "$f" -o "svg/$base.svg" -b white -c mermaid-config.json
  mmdc -q -i "$f" -o "png/$base.png" -b white -c mermaid-config.json -s 2
  echo "rendered $base"
done
