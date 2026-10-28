---
title: Listeners
description: Блок listeners — входы ядра; в miray на остановке закрываются все входы, а QUIC-входы закрывают соединения явно.
lang: ru
order: 11
section: inbound
mihomo: https://wiki.metacubex.one/ru/config/inbound/listeners/
status: changed
since: v1.19.33-miray.1
---

# Listeners

Входы ядра, каждый со своим типом, адресом и портом. Общие поля (`name`, `type`, `listen`, `port`, `routing-mark`, `rule`, `proxy`) — как в [mihomo](https://wiki.metacubex.one/ru/config/inbound/listeners/).

## Остановка и reload

На reload (`PUT /configs`) вход, у которого не изменилась настройка, продолжает работать. Вход с изменённой настройкой и вход, которого нет в новом конфиге, закрываются.

> **miray:** на остановке ядра закрываются все входы `listeners:`, а не только TUN. QUIC-входы ([hysteria2](hysteria2.md), [tuic](tuic.md), trusttunnel с QUIC) закрывают свои соединения кадром CONNECTION_CLOSE — и на остановке, и на reload, — как sing-box. Клиент узнаёт о закрытии сразу и переподключается. В mihomo соединения бросаются молча, и клиент ждёт таймаута простоя (у клиента hysteria — 30 с).

Входы с отличиями в miray:

- [hysteria2](hysteria2.md)
- [tuic](tuic.md)

Остальные типы настраиваются как в mihomo.
