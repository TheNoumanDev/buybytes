#!/bin/sh
# Renders README screenshots from a made-up machine (never your real files).
# Needs freeze: go install github.com/charmbracelet/freeze@v0.2.2
# usage: scripts/screenshot.sh <list|scan|scanning|done> <outdir>
# freeze sometimes hangs, so each try is killed after 40s.
s=$1; S=$2
./buybytes demo-screen "$s" > "$S/$s.ansi" || exit 1
for try in 1 2 3; do
  perl -e 'my $p=fork; if(!$p){exec @ARGV; exit 1} $SIG{ALRM}=sub{kill 9,$p}; alarm 40; waitpid $p,0; exit($? ? 1 : 0)' \
    "$HOME/go/bin/freeze" --execute "cat $S/$s.ansi" -o "$S/$s.png" --window -b "#16161e" -p 20,24 -m 24 -r 10 \
    --font.size 14 --line-height 1.3 --shadow.blur 20 --shadow.y 8 >/dev/null 2>&1 && break
  echo "retry $s"
done
[ -f "$S/$s.png" ] && sips -Z 1800 "$S/$s.png" >/dev/null && echo "ok $s"
