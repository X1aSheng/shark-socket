# ADR-007：Docker 构建代理可配置

状态：已采纳

## 背景

云服务器和企业网络环境可能无法稳定访问默认 Go module proxy。构建网络问题不应阻塞镜像生产。

## 决策

Dockerfile 暴露 `GOPROXY` build arg，docker-compose 可以提供默认代理值，用户可覆盖。

## 原因

- 提升云端构建稳定性。
- 兼容企业内部代理。
- 不影响本地默认构建。

## 后果

部署文档必须说明 `GOPROXY` 的默认值和覆盖方式。

## 重新评估条件

若构建系统改为完全离线 vendor 或内部制品库，可重新评估默认代理策略。

## 关联文档

- [DEPLOYMENT.md](../DEPLOYMENT.md)
- [CONFIGURATION.md (见 guides/CONFIGURATION-20260530.md)](../CONFIGURATION.md)
