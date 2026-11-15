#!/bin/sh
# check-window.sh APPIMAGE: run it on $DISPLAY and wait (60 s at most) for its
# "desk" window, as the AppImage catalog's test does; fails if it exits first.
"$1" &
p=$!
for i in $(seq 60); do
	xwininfo -root -tree | grep -q '"desk"' && exit 0
	kill -0 $p || exit 1
	sleep 1
done
exit 1
