#!/usr/bin/env sh
# Create an age key pair for sops, in the place sops looks for it, and print
# the public key to paste into .sops.yaml (docs/secrets-management-runbook.md
# section 9).
#
# age has two halves:
#   - the public key (recipient), starts with "age1". Anyone may have it. It
#     goes into .sops.yaml and is used to encrypt.
#   - the private key (identity), starts with "AGE-SECRET-KEY-1". Only holders
#     can decrypt. It lives in keys.txt below and is never committed.
#
# sops finds the private key in $SOPS_AGE_KEY_FILE, or by default in
#   macOS:  ~/Library/Application Support/sops/age/keys.txt
#   Linux:  ~/.config/sops/age/keys.txt
set -eu

case "$(uname -s)" in
  Darwin) default_dir="$HOME/Library/Application Support/sops/age" ;;
  *)      default_dir="${XDG_CONFIG_HOME:-$HOME/.config}/sops/age" ;;
esac
key_file=${SOPS_AGE_KEY_FILE:-$default_dir/keys.txt}

if [ -f "$key_file" ]; then
  echo "key file already exists: $key_file"
else
  mkdir -p "$(dirname "$key_file")"
  age-keygen -o "$key_file"
  chmod 600 "$key_file"
  echo "created $key_file"
fi

echo
echo "public key (paste into .sops.yaml as the age recipient):"
age-keygen -y "$key_file"
