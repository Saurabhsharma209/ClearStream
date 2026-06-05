# SIPp (SIP load / simulation)

Homebrew package **`sipp`** — used for bidirectional SIP/RTP tests with noisy or scripted media.

## Install

```bash
bash ../setup/install_deps.sh
sipp -v
```

## Quick UAC test against local Asterisk

```bash
# Default echo/uac scenario — adjust extension and IP
# -trace_msg / -trace_err for SIP; RTP can be captured with tcpdump on :5060/:10000
sipp -sn uac -d 30000 -s 1000 127.0.0.1:5060 -m 1

# Capture SIP/RTP to PCAP for Wireshark (while ClearStream also writes in/out under PCAP_DIR)
tcpdump -i any -w voice-qa/sipp/capture.pcap udp port 5060 or portrange 10000-20000
```

Add project-specific XML scenarios in this folder (e.g. `noisy_uac.xml`) and document ports matching `voice-qa/setup/env.local`.

## Note

Meeting notes may say "Sippy"; on macOS the standard tool is **SIPp** (`brew install sipp`).
