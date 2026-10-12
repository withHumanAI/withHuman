#!/bin/sh
# Print the licence and notice files of the third-party code a release ships.
#
#   sh scripts/third-party-notices.sh go ./cmd/withhuman      Go modules a binary compiles in
#   sh scripts/third-party-notices.sh npm frontend            the frontend's production packages
#
# Go mode needs the modules downloaded (go mod download); npm mode needs
# node_modules installed. A dependency without a licence file is listed with
# a pointer to its source so the gap is visible rather than silent.
set -eu

license_files() {
  find "$1" -maxdepth 1 -type f | grep -iE '/(licen[cs]e|copying|notice)([.-][^/]*)?$' | sort
}

print_entry() {
  name=$1
  dir=$2
  source=$3
  printf '================================================================\n%s\n%s\n================================================================\n\n' "$name" "$source"
  files=$(license_files "$dir")
  if [ -z "$files" ]; then
    printf 'No licence file is included in this package; see its source above.\n\n'
    return
  fi
  for file in $files; do
    cat "$file"
    printf '\n\n'
  done
}

mode=${1:-}
[ "$#" -ge 2 ] || { echo "usage: $0 go <package>... | npm <directory>" >&2; exit 2; }
shift

case "$mode" in
go)
  print_entry "Go standard library" "$(go env GOROOT)" "https://go.dev/"
  go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' "$@" |
    sort -u |
    while read -r path version dir; do
      print_entry "$path $version" "$dir" "https://pkg.go.dev/$path@$version"
    done
  ;;
npm)
  root=$(cd "$1" && pwd)
  (cd "$root" && npm ls --omit=dev --all --parseable) |
    sed 1d |
    sort -u |
    while read -r dir; do
      id=$(node -p 'const p = require(process.argv[1]); p.name + " " + p.version' "$dir/package.json")
      print_entry "$id" "$dir" "https://www.npmjs.com/package/${id% *}"
    done
  ;;
*)
  echo "unknown mode: $mode" >&2
  exit 2
  ;;
esac
