---
title: Документация miray
description: Конфигурация ядра miray — чем она отличается от mihomo и свои опции miray.
lang: ru
order: 0
---

# Документация miray

miray — форк ядра [mihomo](https://github.com/MetaCubeX/mihomo) (Clash.Meta). Конфиг совместим с mihomo: что не описано здесь, работает, как в [вики mihomo](https://wiki.metacubex.one/ru/config/).

- [Карта отличий](differences.md) — по каждому блоку конфига: страница miray или ссылка на mihomo.
- [Блок `miray`](config/miray/index.md) — опции, которых нет в mihomo.
- [TUN](config/inbound/tun.md)
- [Listeners](config/inbound/listeners/index.md): hysteria2, tuic
- [Прокси](config/proxies/index.md): openvpn, trusttunnel
- [Группы прокси](config/proxy-groups/index.md): url-test, fallback, load-balance

Что изменено в ядре без изменений в конфиге (скорость, исправления протоколов) — в [README ядра](../README.md).
