#!/bin/bash
set -e

APP_DIR="/opt/pr-collector"
BINARY="pr-collector"

echo "=== PR Collector 一键部署 ==="

# 1. 编译
echo "[1/5] Building..."
CGO_ENABLED=0 go build -ldflags="-s -w" -o "$BINARY" .

# 2. 创建目录
echo "[2/5] Creating directories..."
sudo mkdir -p "$APP_DIR/logs"

# 3. 复制文件
echo "[3/5] Copying files..."
sudo cp "$BINARY" "$APP_DIR/"
if [ ! -f "$APP_DIR/config.yaml" ]; then
    sudo cp config.example.yaml "$APP_DIR/config.yaml"
    echo "  -> 已创建 config.yaml，请编辑填入 GitHub Token: $APP_DIR/config.yaml"
else
    echo "  -> config.yaml 已存在，跳过"
fi

# 4. 安装 systemd 服务
echo "[4/5] Installing systemd service..."
sudo cp deploy/pr-collector.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable pr-collector

# 5. 启动
echo "[5/5] Starting service..."
sudo systemctl restart pr-collector
sleep 2
sudo systemctl status pr-collector --no-pager

echo ""
echo "部署完成！"
echo "  查看日志: journalctl -u pr-collector -f"
echo "  编辑配置: vim $APP_DIR/config.yaml 后 systemctl restart pr-collector"
