#!/usr/bin/env bash
# Push text to your LOCAL (Mac) clipboard from the remote host via OSC 52.
# Works in Termius when clipboard access is allowed. No mouse selection needed.
#
#   ./sample/clip.sh "some text"      # copy an argument
#   echo "hello" | ./sample/clip.sh   # copy stdin
#   klog '...' app.log | ./sample/clip.sh
set -euo pipefail

if [[ $# -gt 0 ]]; then
  data="$*"
else
  data="$(cat)"
fi

b64="$(printf '%s' "$data" | base64 | tr -d '\n')"

# If inside tmux, wrap so tmux passes the escape through to the outer terminal.
if [[ -n "${TMUX:-}" ]]; then
  printf '\ePtmux;\e\e]52;c;%s\a\e\\' "$b64"
else
  printf '\e]52;c;%s\a' "$b64"
fi

echo "copied ${#data} chars to your Mac clipboard (paste with Cmd-V)" >&2
