---
title: Прокси
description: Блок proxies — общие поля узла и протоколы, которые в miray работают иначе.
lang: ru
order: 20
section: proxies
mihomo: https://wiki.metacubex.one/ru/config/proxies/
status: changed
since: v1.19.33-miray.1
---

# Прокси

Блок `proxies` — список узлов. Ниже — поля, общие для всех типов; у каждого протокола есть ещё свои.

**Отличия miray коротко:** адреса сервера перебираются по RFC 8305 — со сдвигом 250 мс, IPv6 первым, если не задано `ip-version: ipv4-prefer` (см. [`ip-version`](#ip-version)).

```yaml
proxies:
  - name: ss1
    type: ss
    server: example.com
    port: 443
    ip-version: dual
    udp: true
    interface-name: eth0
    routing-mark: 1234
    tfo: false
    mptcp: false
    dialer-proxy: ss2
    smux:
      enabled: true
      protocol: smux
      max-connections: 4
      min-streams: 4
```

## name

Имя узла, обязательно и уникально: по нему узел указывают в группах и правилах.

## type

Протокол узла, обязательно: `ss`, `vmess`, `vless`, `trojan`, `hysteria2`, `tuic`, `direct`, …

## server, port

Адрес сервера (домен или IP) и порт, обязательно.

## ip-version

Какие адреса сервера использовать, если `server` — домен (у `direct` — адреса назначения): `dual` (по умолчанию), `ipv4`, `ipv6`, `ipv4-prefer`, `ipv6-prefer`. `ipv4` и `ipv6` — только это семейство; с `-prefer` — оба, предпочтительное первым.

> **miray:** при нескольких адресах они перебираются по Happy Eyeballs (RFC 8305): сначала адрес предпочтительного семейства, затем семейства чередуются; следующая попытка — через 250 мс после предыдущей или сразу после её отказа; первое соединение выигрывает, остальные попытки отменяются. Предпочтительное семейство у `dual` и `ipv6-prefer` — IPv6, у `ipv4-prefer` — IPv4. В mihomo у `dual` IPv4 и IPv6 стартовали одновременно, у `-prefer` второе семейство — через 300 мс, а адреса одного семейства шли по очереди, каждому — доля таймаута. Общий параметр [`tcp-concurrent`](../general.md#tcp-concurrent) на перебор больше не влияет.

## udp

Пускать ли UDP через узел. По умолчанию `false`; у протоколов поверх UDP (hysteria2, tuic, …), `direct` и `dns` — включено.

## interface-name

Через какой сетевой интерфейс узел подключается к серверу.

## routing-mark

Метка (fwmark) соединений узла к серверу, Linux.

## tfo, mptcp

TCP Fast Open и Multipath TCP для соединений к серверу; только для протоколов поверх TCP.

## dialer-proxy

Подключаться к серверу узла не напрямую, а через другой узел или группу (цепочка). Значение — имя узла или группы.

## smux

Мультиплексирование sing-mux поверх одного соединения; для протоколов поверх TCP.

- `enabled` — включить.
- `protocol` — `h2mux` (по умолчанию), `smux` или `yamux`.
- `max-connections`, `min-streams` — сколько соединений держать и сколько потоков в соединении, прежде чем открыть следующее; `max-streams` — предел потоков на соединение. `max-streams` не сочетается с первыми двумя.
- `statistic` — показывать нижнее соединение в списке соединений API.
- `only-tcp` — мультиплексировать только TCP, UDP — обычным путём протокола.
- `padding` — дополнять кадры.
- `brutal-opts` — TCP Brutal: `enabled`, `up`, `down` (скорость).

## Протоколы

Протоколы, у которых в miray другое поведение при тех же значениях:

- [openvpn](openvpn.md) — `auth` по умолчанию SHA1;
- [trusttunnel](trusttunnel.md) — туннель не обрывается сервером, пока по нему идут данные; по QUIC — keepalive.

Остальные протоколы настраиваются как в mihomo.
