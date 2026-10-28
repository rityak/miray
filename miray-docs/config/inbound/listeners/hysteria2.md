---
title: Hysteria2
description: Вход hysteria2; в miray на остановке и reload закрывает соединения кадром CONNECTION_CLOSE.
lang: ru
order: 12
section: inbound
mihomo: https://wiki.metacubex.one/ru/config/inbound/listeners/hysteria2/
status: changed
since: v1.19.33-miray.1
---

# Hysteria2

Сервер протокола Hysteria2 поверх QUIC.

```yaml
listeners:
  - name: hy2-in
    type: hysteria2
    port: 8443
    listen: 0.0.0.0
    users:
      user1: password1
    up: 1000
    down: 1000
    # obfs: salamander
    # obfs-password: password
    # masquerade: https://example.com
    certificate: ./server.crt
    private-key: ./server.key
```

Общие поля входа — [listeners](index.md).

> **miray:** на остановке ядра и на reload, который пересоздаёт вход, соединения клиентов закрываются кадром CONNECTION_CLOSE, и клиент переподключается сразу. В mihomo клиент ждёт таймаута простоя. Подробно — [остановка и reload](index.md#остановка-и-reload).

## users

Пользователи: `имя: пароль`. Клиент входит паролем; имя — то, что видят правила `IN-USER`.

## up / down

Скорость сервера в мегабитах в секунду (число) или строкой с единицами (`100 Mbps`). `up` — к клиенту, `down` — от клиента. Не задано — без ограничения со стороны сервера.

Если клиент сообщил свою скорость приёма и [`ignore-client-bandwidth`](#ignore-client-bandwidth) выключен, сервер отправляет с этой скоростью (алгоритм Brutal), но не быстрее `up`. Если клиент скорость не сообщил — BBR.

## ignore-client-bandwidth

Не брать скорость клиента: всегда BBR. Если при этом задан `down`, клиент, который не сообщил свою скорость, не проходит вход.

## obfs

Обфускация пакетов: `salamander` или `gecko`. Требует `obfs-password`. Не задано — без обфускации.

## obfs-password

Пароль обфускации; должен совпадать у клиента.

## obfs-min-packet-size / obfs-max-packet-size

Размеры пакетов для `gecko`. По умолчанию 512 и 1200.

## masquerade

Что отвечать на HTTP/3-запросы без верного пароля, чтобы сервер выглядел как сайт:

- `file:///путь` — отдавать файлы из каталога;
- `http://…` или `https://…` — проксировать запрос на этот адрес.

Не задано — ответ 404.

## realm-opts

Регистрация сервера в Hysteria Realm (`enable`, `server-url`, `token`, `realm-id`, `stun-servers`, TLS-поля сервера Realm, `proxy`) — как в [mihomo](https://wiki.metacubex.one/ru/config/inbound/listeners/hysteria2-realm/).

## alpn

ALPN сервера. По умолчанию `h3`.

## certificate / private-key

Сертификат и закрытый ключ сервера: PEM строкой или путь к файлу. Обязательны.

## client-auth-type / client-auth-cert

Проверка сертификата клиента: `request`, `require-any`, `verify-if-given`, `require-and-verify`; не задано — не запрашивать. `client-auth-cert` — сертификаты для проверки (PEM или путь).

## ech-key

Ключ ECH сервера.

## cwnd / bbr-profile

Начальное окно (в пакетах) и профиль (`conservative`, `standard`, `aggressive`) для BBR.

## udp-mtu

Максимальный размер UDP-датаграммы в туннеле. По умолчанию 1197 (1200 у QUIC минус заголовок кадра).

## initial-stream-receive-window / max-stream-receive-window / initial-connection-receive-window / max-connection-receive-window

Окна приёма QUIC, байты. Не задано — умолчания QUIC-библиотеки.

## max-idle-time

Принимается, но сервер hysteria2 его не применяет — так же в mihomo.

## mux-option

Мультиплексирование sing-mux поверх входа — как в [mihomo](https://wiki.metacubex.one/ru/config/inbound/listeners/).
