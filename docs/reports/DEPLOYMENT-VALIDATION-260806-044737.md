# 部署验证报告 (V5 Audit) - shark-mqtt

- 日期: 2026-08-06 04:47
- 环境: Windows 11 本地开发机 (无 docker / kubectl / helm / 云服务器)

## 1. Linux 编译验证

`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/` -> PASS, 静态 ELF。

## 2. Docker
- 多阶段构建, 非 root, HEALTHCHECK 探测 :18999/healthz, 暴露 18983/18993/18999。
- CI `docker` job 执行 build + 启动 + healthz + MQTT smoke test。
- 本机无 docker, `docker build` 未实际执行。

## 3. Kubernetes
- `deploy/k8s/app/` 全部 YAML 语法校验 OK。
- Helm chart (`deploy/k8s/helm/shark-mqtt/`) 为标准 Go 模板, 需 `helm template` 渲染。

## 4. 云服务器部署流程
```bash
scp shark-mqtt-linux root@<HOST>:/usr/local/bin/shark-mqtt
shark-mqtt -addr=:18983            # 生产配置认证, 勿用 -allow-all
mosquitto_pub -h <HOST> -p 18983 -t test -m hello
mosquitto_sub -h <HOST> -p 18983 -t test -C 1
curl -sf http://127.0.0.1:18999/healthz
docker build -f deploy/docker/Dockerfile -t shark-mqtt:latest .
kubectl apply -k deploy/k8s/app/
```

## 5. 结论
编译验证通过; docker/k8s 实际部署需在有 docker + kubectl 的服务器执行。
