#!/usr/bin/env bash
# Builds BuildCaptain and installs it in a folder on PATH.
set -euo pipefail

cd "$(dirname "$0")"

install_dir="${INSTALL_DIR:-/usr/local/bin}"
binary_name="buildcaptain"

./build.sh

if [[ ! -d "$install_dir" ]]; then
  if [[ -w "$(dirname "$install_dir")" ]]; then
    mkdir -p "$install_dir"
  else
    sudo mkdir -p "$install_dir"
  fi
fi

if [[ -w "$install_dir" ]]; then
  install -m 0755 "$binary_name" "$install_dir/$binary_name"
else
  sudo install -m 0755 "$binary_name" "$install_dir/$binary_name"
fi

echo "Installed $binary_name to $install_dir/$binary_name"
echo "Run '$binary_name version' to verify the installation."
