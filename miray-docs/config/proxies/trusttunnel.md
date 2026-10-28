---
title: TrustTunnel
description: Клиент TrustTunnel (HTTP/2 или HTTP/3); в miray туннель не обрывается сервером, пока по нему идут данные, а по QUIC держится keepalive.
lang: ru
order: 22
section: proxies
mihomo: https://wiki.metacubex.one/ru/config/proxies/trusttunnel/
status: changed
since: v1.19.33-miray.1
---

# TrustTunnel

Клиент TrustTunnel: каждое соединение — поток `CONNECT` поверх HTTP/2 через TLS или поверх HTTP/3 (QUIC). Несколько потоков идут через одно соединение с сервером; сколько соединений открывать — [`max-connections`](#max-connections).

```yaml
proxies:
  - name: tt
    type: trusttunnel
    server: 1.2.3.4
    port: 443
    username: username
    password: password
    # sni: example.com
    # client-fingerprint: chrome
    health-check: true
    udp: true
    # quic: true
    # congestion-controller: bbr
```

Общие поля прокси (`name`, `interface-name`, `dialer-proxy`, `ip-version` …) — как в [mihomo](https://wiki.metacubex.one/ru/config/proxies/).

> **miray:** сервер TrustTunnel закрывает соединение клиента, по которому за `client_listener_timeout` (по умолчанию 600 с) не открылось ни одного нового потока, — вместе с потоками, по которым идут данные. В mihomo без [`health-check`](#health-check) длинная загрузка обрывается раз в 10 минут. В miray, пока по потокам соединения идут данные, клиент сам отправляет `_check` раз в 30 с, как эталонный клиент; без трафика — не отправляет. С `health-check: true` поведение прежнее.

## server

Адрес сервера.

## port

Порт сервера.

## username / password

Логин и пароль на сервере.

## health-check

Проверять соединение запросом `_check`, если 7 с не открывалось новых потоков. Не задано — не проверять (но см. отметку miray выше).

## udp

Разрешить UDP через прокси.

## TLS

`sni` (по умолчанию — `server`), `alpn`, `skip-cert-verify`, `fingerprint`, `certificate` / `private-key`, `client-fingerprint`, `ech-opts` — как у TLS-полей прокси в [mihomo](https://wiki.metacubex.one/ru/config/proxies/tls/). `alpn` по умолчанию `h2`, с `quic` — `h3`; список без нужного протокола — ошибка конфига.

## quic

Работать поверх HTTP/3 (QUIC) вместо HTTP/2. По умолчанию — HTTP/2.

> **miray:** соединение QUIC отправляет PING раз в 10 с, как клиент HTTP/3 по умолчанию. В mihomo свой QUIC-конфиг этого keepalive не наследует, и соединение с открытыми, но молчащими потоками закрывается по таймауту простоя (74 с).

## congestion-controller

Алгоритм перегрузки для QUIC: `cubic`, `new_reno`, `bbr_meta_v1`, `bbr` (он же `bbr_meta_v2`). Не задано — по умолчанию QUIC-библиотеки.

### cwnd

Начальное окно перегрузки для `bbr` и `bbr_meta_v1`, в пакетах. По умолчанию 32.

### bbr-profile

Профиль `bbr`: `conservative`, `standard`, `aggressive`.

## max-connections

Сколько соединений с сервером держать. Поток идёт через соединение с наименьшим числом потоков; новое соединение открывается, если в нём уже не меньше [`min-streams`](#min-streams) потоков и соединений меньше `max-connections`.

Если `max-connections`, `min-streams` и `max-streams` все не заданы — 8 соединений и 5 потоков.

## min-streams

Сколько потоков должно быть в соединении, прежде чем открыть следующее. Работает вместе с `max-connections`.

## max-streams

Без `max-connections`: сколько потоков можно открыть в одном соединении, дальше — новое соединение.
