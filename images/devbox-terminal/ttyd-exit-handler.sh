#!/bin/bash
# Supervisord event listener — kills supervisord when ttyd exits.
# Protocol: print READY, read header+payload, respond RESULT 2\nOK.
# If ttyd exited, bring down the container.
while true; do
    echo "READY"
    read header
    # Read payload (length from header)
    len=$(echo "$header" | tr ' ' '\n' | grep ^len: | cut -d: -f2)
    payload=$(head -c "$len")
    if echo "$payload" | grep -q "processname:ttyd"; then
        echo "RESULT 2"
        echo "OK"
        kill -TERM $(cat /tmp/supervisord.pid) 2>/dev/null
        exit 0
    fi
    echo "RESULT 2"
    echo "OK"
done
