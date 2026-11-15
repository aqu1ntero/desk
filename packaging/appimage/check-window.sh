#!/bin/sh
# check-window.sh APPIMAGE: run it on $DISPLAY and wait (60 s at most) for its
# "desk" window, as the AppImage catalog's test does; fails if it exits first,
# saying how and showing desk's crash log.
"$1" &
p=$!
for i in $(seq 60); do
	xwininfo -root -tree | grep -q '"desk"' && exit 0
	if ! kill -0 $p 2>/dev/null; then
		wait $p
		echo "desk exited ($?) without a window"
		break
	fi
	sleep 1
done
kill -0 $p 2>/dev/null && echo "no desk window after 60 s"
find "$HOME" -name crash.log -exec cat {} + 2>/dev/null
xwininfo -root -tree | head -20
exit 1
