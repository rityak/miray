---
title: Прокси
description: Блок proxies — какие протоколы в miray работают иначе, чем в mihomo.
lang: ru
order: 20
section: proxies
mihomo: https://wiki.metacubex.one/ru/config/proxies/
status: changed
since: v1.19.33-miray.1
---

# Прокси

Общие поля прокси (`name`, `type`, `server`, `port`, `udp`, `dialer-proxy`, `smux` …) в miray такие же, как в mihomo — [общие поля в вики mihomo](https://wiki.metacubex.one/ru/config/proxies/).

Протоколы, у которых в miray другое поведение при тех же значениях:

- [openvpn](openvpn.md) — `auth` по умолчанию SHA1;
- [trusttunnel](trusttunnel.md) — туннель не обрывается сервером, пока по нему идут данные; по QUIC — keepalive.

Остальные протоколы настраиваются как в mihomo.
