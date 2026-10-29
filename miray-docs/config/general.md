---
title: Общие настройки
description: Верхний уровень конфига — локальная сеть, режим, лог, API, дозвон, GEO-файлы.
lang: ru
order: 5
section: general
mihomo: https://wiki.metacubex.one/ru/config/general/
status: changed
since: v1.19.33-miray.2
---

# Общие настройки

Параметры верхнего уровня конфига. Порты входов (`port`, `socks-port`, `mixed-port`, …) описаны в [вики mihomo](https://wiki.metacubex.one/ru/config/inbound/port/), TUN — на [своей странице](inbound/tun.md), блок `tls` — [здесь](tls.md), свои опции miray — в [блоке `miray`](miray/index.md).

**Отличия miray коротко:** адреса назначения перебираются по RFC 8305 — со сдвигом 250 мс, семейства по очереди, IPv6 первым; `tcp-concurrent` на это больше не влияет. Подробно — у [`tcp-concurrent`](#tcp-concurrent).

## allow-lan

Принимать ли на портах прокси соединения с других устройств, а не только с этого компьютера. По умолчанию `false`.

```yaml
allow-lan: true
bind-address: "*"
lan-allowed-ips: [0.0.0.0/0, "::/0"]
lan-disallowed-ips: [192.168.0.3/32]
```

- `bind-address` — на каком адресе слушать: `"*"` (по умолчанию) — на всех, иначе один IPv4 или IPv6 (`[aaaa::1]`).
- `lan-allowed-ips` — с каких адресов принимать, когда `allow-lan: true`; по умолчанию все (`0.0.0.0/0`, `::/0`).
- `lan-disallowed-ips` — с каких не принимать; важнее `lan-allowed-ips`. По умолчанию пусто.

## authentication

Логины и пароли для входов `http`, `socks`, `mixed`; `skip-auth-prefixes` — адреса, которым пароль не нужен.

```yaml
authentication: ["user1:pass1", "user2:pass2"]
skip-auth-prefixes: [127.0.0.1/8, "::1/128"]
```

## mode

Как выбирается выход: `rule` (по умолчанию) — по правилам `rules`, `global` — всё через группу `GLOBAL`, `direct` — всё напрямую.

## log-level

Подробность лога: `silent`, `error`, `warning`, `info` (по умолчанию), `debug`.

## ipv6

Принимать ли ядру трафик IPv6. По умолчанию `true`. Получать ли адреса IPv6 из DNS — `dns.ipv6`, по умолчанию `false`: без него у назначения по домену есть только IPv4.

## keep-alive-idle, keep-alive-interval, disable-keep-alive

TCP keepalive исходящих соединений: через сколько секунд простоя слать первую пробу и с каким интервалом следующие. `0` (по умолчанию) — умолчание Go, 15 с. `disable-keep-alive: true` выключает keepalive; на Android он выключен всегда.

## find-process-mode

Определять ли процесс-источник соединения (для правил `PROCESS-NAME` и в API): `strict` (по умолчанию) — только если в правилах есть такие условия, `always` — всегда, `off` — никогда (для роутеров).

## external-controller

Адрес REST API, например `127.0.0.1:9090`. Описание API и отличия reload в miray — на [странице API](../api/index.md).

```yaml
external-controller: 127.0.0.1:9090
secret: ""
external-controller-cors:
  allow-origins: ["*"]
  allow-private-network: true
```

- `secret` — ключ доступа к API.
- `external-controller-tls` — API по HTTPS на отдельном адресе; сертификат — в блоке [`tls`](tls.md).
- `external-controller-unix`, `external-controller-pipe` — API через Unix-сокет или именованный канал Windows. Ключ `secret` там не проверяется.
- `external-controller-routing-mark` — routing mark сокетов API (Linux).
- `external-doh-server` — путь DoH-сервера на порту API, например `/dns-query`; ключ не проверяется.

## external-ui

Папка статической панели управления, отдаётся по адресу API `/ui`. Путь абсолютный или относительно рабочей папки; папка вне рабочей — только если она указана в переменной окружения `SAFE_PATHS`. `external-ui-name` — подпапка внутри `external-ui`, `external-ui-url` — откуда скачивать панель при обновлении через API.

## profile

```yaml
profile:
  store-selected: true
  store-fake-ip: false
```

- `store-selected` — помнить выбор в группах `select` между запусками. По умолчанию `true`.
- `store-fake-ip` — помнить соответствие fake-ip и доменов между запусками. По умолчанию `false`.

## unified-delay

Задержка проверки узла — время одного обмена после установленного соединения, без рукопожатий, чтобы узлы разных протоколов сравнивались честно. По умолчанию `false`.

## tcp-concurrent

В mihomo — дозваниваться на все адреса назначения сразу. По умолчанию `false`.

> **miray:** параметр ни на что не влияет: адреса всегда перебираются по Happy Eyeballs (RFC 8305 §4–5). Сначала адрес предпочтительного семейства, затем семейства чередуются; каждая следующая попытка стартует через 250 мс после предыдущей или сразу, как предыдущая получила отказ; первое установленное соединение выигрывает, остальные попытки отменяются. Предпочтительное семейство — IPv6, а при `ip-version: ipv4-prefer` у прокси — IPv4 (см. [общие поля прокси](proxies/index.md#ip-version)). В mihomo без `tcp-concurrent` адреса одного семейства шли по очереди, и неотвечающий первый адрес съедал долю таймаута (секунды), а IPv4 и IPv6 стартовали одновременно — два SYN на каждое соединение; с `tcp-concurrent` — все адреса сразу. Так перебирает адреса и браузер; цена — лишняя попытка, если первый адрес отвечает дольше 250 мс. Если IPv6 у провайдера сломан, а DNS отдаёт адреса IPv6, соединение с таким назначением начинается на 250 мс позже: выключите `dns.ipv6` или задайте `ip-version: ipv4-prefer`.

## interface-name

Через какой сетевой интерфейс выходят соединения ядра, например `en0`.

## routing-mark

Метка (fwmark) исходящих соединений ядра на Linux, например `6666`.

## global-client-fingerprint

Устарел: отпечаток TLS задаётся у каждого прокси (`client-fingerprint`).

## geodata-mode, geodata-loader

`geodata-mode: true` — GeoIP из файла `.dat`, а не `.mmdb`; по умолчанию `false`. `geodata-loader` — `memconservative` (по умолчанию, меньше памяти) или `standard`.

## geo-auto-update, geo-update-interval, geox-url

Обновлять ли GEO-файлы сами (по умолчанию `false`), раз в сколько часов (по умолчанию 24) и откуда:

```yaml
geox-url:
  geoip: "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat"
  geosite: "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat"
  mmdb: "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.metadb"
  asn: "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/GeoLite2-ASN.mmdb"
```

## global-ua

User-Agent при скачивании providers, GEO-файлов и панели. По умолчанию `clash.meta/<версия>`.

## etag-support

Учитывать ли ETag при скачивании внешних ресурсов: не скачивать неизменившийся файл. По умолчанию `true`.
