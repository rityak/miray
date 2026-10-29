---
title: hysteria2
description: Прокси hysteria2 — поля, прыжки по портам; что в miray работает иначе.
lang: ru
order: 25
section: proxies
mihomo: https://wiki.metacubex.one/ru/config/proxies/hysteria2/
status: changed
since: v1.19.33-miray.2
---

# hysteria2

```yaml
proxies:
  - name: hy2
    type: hysteria2
    server: server.example
    port: 443
    ports: 20000-20010
    hop-interval: 30
    password: secret
    up: "30 Mbps"
    down: "200 Mbps"
    obfs: salamander
    obfs-password: secret
    sni: server.example
    skip-cert-verify: false
    alpn: [h3]
```

Общие поля узла — на [странице прокси](index.md); TLS (`sni`, `skip-cert-verify`, `name-cert-verify`, `fingerprint`, `certificate`, `private-key`, `alpn`, `ech-opts`) — как у остальных протоколов поверх TLS.

**Отличия miray коротко:** при прыжках по портам закрытый порт диапазона не обрывает соединение — клиент сразу переходит на другой порт; после рестарта сервера клиент переподключается сразу, а не через idle-таймаут. Подробно — у [`ports`](#ports).

## ports

Диапазоны портов сервера (`20000-20010`, `443,8443-8450`) — клиент переключается между ними (прыжки по портам); `port` тогда не используется.

> **miray:** если сервер ответил на текущий порт ICMP «порт недоступен» (порт из диапазона закрыт или занят чужим), клиент сразу возвращает соединение на последний работавший порт, а отказавший больше не выбирает. Соединение закрывается, только если отказал и работавший порт: значит, сервер перезапускается, и клиент переподключается сразу, а не через idle-таймаут. В mihomo (и у клиента sing-box) один закрытый порт рвал соединение; официальный клиент hysteria его держит, но теряет датаграммы до следующего прыжка. Работает на Linux.

## hop-interval

Через сколько секунд прыгать на следующий порт. По умолчанию 30, не меньше 5. Диапазон (`15-30`) — каждый раз случайное значение из него.

## password

Пароль на сервере.

## up, down

Скорость для Brutal (по умолчанию в Мбит/с, можно `"30 Mbps"`). Не задано — сервер и клиент договариваются сами, управление перегрузкой — BBR (`bbr-profile`: `standard`, `conservative`, `aggressive`; `cwnd` — начальное окно в пакетах, по умолчанию 32).

## obfs, obfs-password

Обфускация QUIC: `salamander` или `gecko`, пусто — без неё. `obfs-min-packet-size`, `obfs-max-packet-size` — размер пакетов, только для `gecko`.

## udp-mtu

Наибольший размер датаграммы UDP, которую клиент кладёт в одну датаграмму QUIC. По умолчанию 1197.

## handshake-timeout

Тайм-аут рукопожатия QUIC в секундах, отдельно от тайм-аута соединения. По умолчанию 0 — только тайм-аут соединения.

> **miray:** если сервер не слушает порт (перезапускается), клиент на Linux узнаёт это из ICMP «порт закрыт» сразу, а не по idle-таймауту.

## realm-opts

Подключение к серверу за NAT через сервис встречи (realm): `enable`, `server-url`, `token`, `realm-id`, `stun-servers` и TLS-поля для `server-url`.

## initial-stream-receive-window, max-stream-receive-window, initial-connection-receive-window, max-connection-receive-window

Окна приёма QUIC на поток и на соединение. Без нужды не менять.
