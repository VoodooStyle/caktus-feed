# caktus-feed

Bridge that saves posts from the CaktusJxck WhatsApp Channel (followed by a burner account)
into `feed/YYYY-MM-DD.jsonl`, one JSON object per post, and pushes them here every 10 minutes.

Fields: `time` (RFC3339), `channel`, `id` (server message id, increasing), `text`, `type`, `views`.

Runs in Termux on an Android phone. `setup.sh` builds it, `run.sh` keeps it running,
Termux:Boot starts it after a reboot. Log: `bridge.log`.
