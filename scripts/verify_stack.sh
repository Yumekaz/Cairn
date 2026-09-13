#!/usr/bin/env bash
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
while read -r name revision; do
  [[ -z "$name" || "$name" == \#* ]] && continue
  case "$name" in DURAFLOW|Mini-Docker) ;; *) echo "Unknown locked repository: $name" >&2; exit 1;; esac
  [[ "$revision" =~ ^[0-9a-f]{40}$ ]] || { echo "Invalid revision for $name" >&2; exit 1; }
  actual="$(git -C "$repo_dir/../$name" rev-parse HEAD)"
  [[ "$actual" == "$revision" ]] || { echo "$name: expected $revision, found $actual" >&2; exit 1; }
  [[ -z "$(git -C "$repo_dir/../$name" status --porcelain --untracked-files=normal)" ]] || { echo "$name: uncommitted files; cannot certify locked stack" >&2; exit 1; }
  echo "$name $actual"
done < "$repo_dir/stack.lock"
echo "Cairn $(git -C "$repo_dir" rev-parse HEAD)"
if [[ -n "$(git -C "$repo_dir" status --porcelain --untracked-files=normal)" ]]; then
  echo "Cairn has working-tree changes: record its patch as well as HEAD; this is not a pristine release checkout" >&2
  [[ "${STACK_ALLOW_DIRTY:-0}" == "1" ]] || exit 1
fi
