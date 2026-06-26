#!/bin/bash
set -e

APP_DIR="/opt/pr-collector"
BINARY="pr-collector"
SVC_USER="www"

echo "=== PR Collector 一键部署 ==="

# 1. 编译
echo "[1/6] Building..."

# 2. 创建服务用户
echo "[2/6] Creating service user..."
if ! id "$SVC_USER" &>/dev/null; then
    sudo useradd -r -s /bin/false -d /nonexistent -M "$SVC_USER"
    echo "  用户 $SVC_USER 已创建"
fi

# 3. 创建目录
echo "[3/6] Creating directories..."
sudo mkdir -p "$APP_DIR/logs"

# 4. 复制文件
echo "[4/6] Copying files..."
sudo cp "$BINARY" "$APP_DIR/"
if [ ! -f "config.yaml" ]; then
    echo "错误: config.yaml 不存在，请先创建配置文件后重试"
    exit 1
fi
sudo cp config.yaml "$APP_DIR/"
sudo chown -R "$SVC_USER:$SVC_USER" "$APP_DIR"

# 5. 安装 systemd 服务
echo "[5/6] Installing systemd service..."
sudo cp deploy/pr-collector.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable pr-collector

# 6. 启动
echo "[6/6] Starting service..."
sudo systemctl restart pr-collector
sleep 2
sudo systemctl status pr-collector --no-pager

echo ""
echo "部署完成！"
echo "  查看日志: journalctl -u pr-collector -f"
echo "  编辑配置: nano $APP_DIR/config.yaml 后 systemctl restart pr-collector"
