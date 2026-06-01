#!/bin/bash
# Sends synthetic G.711 RTP packets to ClearStream's RTP listener for testing.
# Usage: ./tools/send_rtp_test.sh [host] [port]
# Requires: python3 (standard library only)

HOST=${1:-localhost}
PORT=${2:-5004}

echo "Sending test RTP to ${HOST}:${PORT} for 5 seconds..."

python3 - <<PYEOF
import socket, struct, time

sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
seq = 0
ts = 0
ssrc = 0xDEADBEEF

for i in range(250):  # 250 * 20ms = 5 seconds
    # RTP header: V=2, P=0, X=0, CC=0, M=0, PT=0 (PCMU)
    header = struct.pack('!BBHII', 0x80, 0, seq & 0xFFFF, ts, ssrc)
    payload = bytes([0xFF] * 160)  # G.711 u-law silence frames
    sock.sendto(header + payload, ('${HOST}', ${PORT}))
    seq += 1
    ts += 160
    time.sleep(0.02)

print('Done sending 250 RTP packets (5 seconds of G.711 silence).')
sock.close()
PYEOF
