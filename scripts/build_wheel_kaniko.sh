#!/bin/sh
# Build the gpustack_higress_plugins wheel from prebuilt wasm artifacts.
#
# Kaniko-compatible counterpart of the heredoc block in the original
# Dockerfile: same steps (metadata for every local wasm, remote OCI plugins
# fetched with oras, manifest, wheel, verification), expressed as a plain
# shell script so no heredocs or bind mounts are needed.
set -eux

python3 -m venv .venv
.venv/bin/pip install --no-cache-dir uv

# Install project dependencies only (skip editable project install --
# manifest.json and plugins/ don't exist yet; wheel is built below)
.venv/bin/uv sync --no-dev --no-install-project

# Generate metadata.txt for local plugins (the builder stages only compile
# .wasm)
find gpustack_higress_plugins/plugins -name "plugin.wasm" | while read wasm_file; do
    plugin_name=$(echo "$wasm_file" | awk -F/ '{print $3}')
    .venv/bin/python scripts/generate_metadata.py "$wasm_file" "$plugin_name"
done

# Fetch remote OCI plugins if configured
if [ -f extensions/remote_plugins.yaml ]; then
    .venv/bin/python scripts/fetch_remote_plugins.py \
        --config extensions/remote_plugins.yaml --oras oras
fi

# Generate manifest.json (includes both local and remote plugins)
.venv/bin/python scripts/generate_manifest.py

# Build wheel package using uv
.venv/bin/uv build --wheel --out-dir /dist

# Verify wheel completeness (fails if any plugin is missing)
.venv/bin/python scripts/verify_whl.py --dist-dir /dist

echo "Built wheel files:"
ls -lh /dist/
