#!/bin/sh
set -e

work="${TMPDIR:-/tmp}/hpatch-nvm-download-$$"
test_bin="$work/bin"
argv_log="$work/argv.log"
proof="$work/evaluated"
cleanup() {
  rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM

# shellcheck source=/dev/null
NVM_ENV=testing . ./install.sh
mkdir -p "$test_bin"
cat >"$test_bin/wget" <<'WGET'
#!/bin/sh
: >"$ARGV_LOG"
for argument in "$@"; do
  printf '%s\n' "$argument" >>"$ARGV_LOG"
done
WGET
chmod +x "$test_bin/wget"
ln -s "$(command -v sed)" "$test_bin/sed"
ln -s "$(command -v touch)" "$test_bin/touch"
export ARGV_LOG="$argv_log"

injected_url="https://example.test/v1\$(touch $proof)/archive"
(PATH="$test_bin"; export PATH; nvm_download "$injected_url" -o -)
if [ -e "$proof" ]; then
  echo "nvm_download evaluated untrusted argument text" >&2
  exit 1
fi
grep -Fxq "$injected_url" "$argv_log" || {
  echo "nvm_download did not preserve the URL as one literal argument" >&2
  exit 1
}

spaced_output="$work/output directory/nvm.sh"
mkdir -p "${spaced_output%/*}"
(PATH="$test_bin"; export PATH; nvm_download -L -C - --progress-bar 'https://example.test/nvm.sh?a=1&b=2' -o "$spaced_output")
grep -Fxq "$spaced_output" "$argv_log" || {
  echo "nvm_download split an output path containing spaces" >&2
  exit 1
}
grep -Fxq -- '-c' "$argv_log"
grep -Fxq -- '--progress=bar' "$argv_log"
grep -Fxq -- '-O' "$argv_log"
if grep -Fxq -- '-L' "$argv_log" || grep -Fxq -- '-C' "$argv_log" || grep -Fxq -- '-' "$argv_log"; then
  echo "nvm_download forwarded untranslated curl arguments" >&2
  exit 1
fi

echo "nvm_download literal argument behavior passed"
