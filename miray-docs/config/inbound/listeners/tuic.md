---
title: TUIC
description: Вход TUIC v5; в miray на остановке и reload закрывает соединения кадром CONNECTION_CLOSE.
lang: ru
order: 13
section: inbound
mihomo: https://wiki.metacubex.one/ru/config/inbound/listeners/tuic-v5/
status: changed
since: v1.19.33-miray.1
---

# TUIC

Сервер протокола TUIC v5 поверх QUIC.

```yaml
listeners:
  - name: tuic-in
    type: tuic
    port: 10004
    listen: 0.0.0.0
    users:
      00000000-0000-0000-0000-000000000000: password
    certificate: ./server.crt
    private-key: ./server.key
    congestion-controller: bbr
    max-idle-time: 15000
    authentication-timeout: 1000
    max-udp-relay-packet-size: 1500
```

Общие поля входа — [listeners](index.md).

> **miray:** на остановке ядра и на reload, который пересоздаёт вход, соединения клиентов закрываются кадром CONNECTION_CLOSE, и клиент переподключается сразу. В mihomo клиент ждёт таймаута простоя. Подробно — [остановка и reload](index.md#остановка-и-reload).

## users

Пользователи TUIC v5: `UUID: пароль`.

`token` вместо `users` — TUIC v4: протокол устарел, в miray не развивается — [вики mihomo](https://wiki.metacubex.one/ru/config/inbound/listeners/tuic-v4/).

## certificate / private-key

Сертификат и закрытый ключ сервера: PEM строкой или путь к файлу. Обязательны.

## congestion-controller

Алгоритм перегрузки QUIC: `cubic`, `new_reno`, `bbr_meta_v1`, `bbr` (он же `bbr_meta_v2`). Не задано — по умолчанию QUIC-библиотеки.

## cwnd / bbr-profile

Начальное окно (в пакетах, по умолчанию 32) и профиль (`conservative`, `standard`, `aggressive`) для BBR.

## max-idle-time

Таймаут простоя соединения QUIC, миллисекунды. По умолчанию 15000.

## authentication-timeout

Сколько ждать входа клиента после подключения, миллисекунды. По умолчанию 1000.

## alpn

ALPN сервера. По умолчанию `h3`.

## max-udp-relay-packet-size

Максимальный размер UDP-пакета, передаваемого датаграммой QUIC. По умолчанию 1500, но не больше, чем помещается в датаграмму 1400 байт с заголовком TUIC.

## client-auth-type / client-auth-cert

Проверка сертификата клиента: `request`, `require-any`, `verify-if-given`, `require-and-verify`; не задано — не запрашивать. `client-auth-cert` — сертификаты для проверки (PEM или путь).

## ech-key

Ключ ECH сервера.

## mux-option

Мультиплексирование sing-mux поверх входа — как в [mihomo](https://wiki.metacubex.one/ru/config/inbound/listeners/).
