#!/bin/sh
# Fuzz every target in the module, for a while each.
#
# The seed corpus runs with every "go test", and that is the part worth having
# in CI. This is the other part: looking for input nobody has thought of yet,
# which takes as long as it is given and so is left to be run by hand.
#
# Usage: scripts/fuzz.sh [fuzztime]   (default 30s per target)
set -eu

fuzztime="${1:-30s}"

# The targets are found rather than listed, so a new one is fuzzed by writing
# it and nothing else.
for pkg in $(go list ./...); do
	for target in $(go test -list 'Fuzz.*' "$pkg" | grep '^Fuzz' || true); do
		echo "== $pkg $target"
		go test "$pkg" -run "$target" -fuzz "$target\$" -fuzztime "$fuzztime"
	done
done
