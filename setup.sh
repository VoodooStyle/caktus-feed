#!/data/data/com.termux/files/usr/bin/bash
# One-time setup: build the bridge and make it start when the phone boots.
set -e
cd "$(dirname "$0")"
pkg install -y git golang clang >/dev/null
go mod tidy
go build -o bridge .
mkdir -p ~/.termux/boot
cat > ~/.termux/boot/start-caktus <<EOF
#!/data/data/com.termux/files/usr/bin/bash
cd $(pwd) && nohup ./run.sh >/dev/null 2>&1 &
EOF
chmod +x ~/.termux/boot/start-caktus
echo "Built OK. Next: ./bridge -phone 1XXXXXXXXXX -nopush"
