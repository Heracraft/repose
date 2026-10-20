mkdir -p ~/.repose
cp "$1/tools/wanted.json" ~/.repose/tools-wanted.json.new
mv -f ~/.repose/tools-wanted.json.new ~/.repose/tools-wanted.json
if command -v repose-tools-install >/dev/null 2>&1; then
  repose-tools-install plan
elif [ ! -x '/opt/repose/bin/repose-agent' ]; then
  echo '#warn The machine has no tool installer, so none of your tools was installed.'
  mkdir -p ~/.repose/carry
  printf '%s:none\n' "$(cat "$1/tools/hash")" > ~/.repose/carry/tools
fi
