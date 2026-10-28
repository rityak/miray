---
title: OpenVPN
description: Клиент OpenVPN (dev tun, UDP или TCP); в miray auth по умолчанию — SHA1, как в OpenVPN.
lang: ru
order: 21
section: proxies
mihomo: https://wiki.metacubex.one/ru/config/proxies/openvpn/
status: changed
since: v1.19.33-miray.1
---

# OpenVPN

Клиент OpenVPN в режиме `dev tun`. Трафик идёт через IP-стек в пространстве пользователя: системный интерфейс не создаётся. Туннель поднимается при первом соединении через прокси.

```yaml
proxies:
  - name: ovpn
    type: openvpn
    server: vpn.example.com
    port: 1194
    proto: udp
    username: user
    password: pass
    ca: |
      -----BEGIN CERTIFICATE-----
      ...
      -----END CERTIFICATE-----
    tls-auth: |
      -----BEGIN OpenVPN Static key V1-----
      ...
      -----END OpenVPN Static key V1-----
    key-direction: "1"
    udp: true
```

Общие поля прокси (`name`, `interface-name`, `routing-mark`, `ip-version` …) — как в [mihomo](https://wiki.metacubex.one/ru/config/proxies/). Значения ключей и сертификатов — содержимое блоков `<ca>`, `<cert>`, `<key>`, `<tls-auth>` из `.ovpn` файла, вставленное строкой.

## server

Адрес сервера OpenVPN (`remote`).

## port

Порт сервера.

## proto

Транспорт: `udp` (по умолчанию) или `tcp`. Принимаются и написания OpenVPN: `udp4`, `tcp4`, `tcp-client`, `tcp4-client`. Другие значения — ошибка конфига.

## username / password

Вход по логину и паролю (`auth-user-pass`). Нужен, если не заданы `cert` и `key`: без того и другого прокси не создаётся.

## ca

Сертификат центра сертификации сервера в PEM. Обязателен.

## cert

Сертификат клиента в PEM. Задаётся вместе с `key`.

## key

Закрытый ключ клиента в PEM. Задаётся вместе с `cert`.

## tls-auth

Статический ключ OpenVPN (256 байт) для подписи управляющего канала. Подпись — HMAC по алгоритму [`auth`](#auth). Нельзя вместе с `tls-crypt` и `tls-crypt-v2`.

## key-direction

Какую половину статического ключа `tls-auth` использовать для отправки: `"1"` (обычно у клиента), `"0"` или пусто — обе стороны одной половиной, как OpenVPN без `key-direction`. Должно совпадать с настройкой сервера (у сервера — противоположное значение).

## tls-crypt

Статический ключ для подписи и шифрования управляющего канала. Нельзя вместе с `tls-auth` и `tls-crypt-v2`.

## tls-crypt-v2

Клиентский ключ tls-crypt-v2. Нельзя вместе с `tls-auth` и `tls-crypt`.

## ping

Интервал в секундах: если клиент ничего не отправлял столько времени, он отправляет пинг. 0 или не задано — не отправляет.

## ping-restart

Секунды без входящих пакетов, после которых туннель поднимается заново. 0 или не задано — не перезапускается по этому признаку.

## peer-info

Дополнительные пары `ключ: значение`, которые клиент сообщает серверу при входе сверх тех, что ядро отправляет само (шифры, сжатие).

## tran-window

Секунды, в течение которых после смены ключей данных принимаются пакеты со старым ключом. По умолчанию 3600, как в OpenVPN. Отрицательное значение — ошибка.

## handshake-timeout

Секунды на подъём туннеля. Не задано — без своего ограничения, до таймаута соединения. Отрицательное значение — ошибка.

## dev

Только `tun` (по умолчанию). `tap` не поддерживается.

## cipher

Шифр данных без [`data-ciphers`](#data-ciphers): `AES-128-GCM` (по умолчанию), `AES-192-GCM`, `AES-256-GCM`, `AES-128-CBC` (или `AES-CBC`), `AES-192-CBC`, `AES-256-CBC`, `CHACHA20-POLY1305`. Если сервер прислал другой шифр, берётся [`data-ciphers-fallback`](#data-ciphers-fallback), а без него — шифр сервера, если он из этого списка.

## data-ciphers

Список шифров данных, которые клиент предлагает серверу. Выбирается первый шифр из присланных сервером, который есть в этом списке; сервер ничего не прислал — [`cipher`](#cipher). Первый шифр списка клиент сообщает серверу как основной.

## data-ciphers-fallback

Шифр на случай, когда общего шифра с сервером нет. Не задано — с [`data-ciphers`](#data-ciphers) это ошибка подключения.

## auth

Алгоритм HMAC для `tls-auth` и шифров без встроенной проверки (CBC): `MD5`, `SHA1` (по умолчанию), `SHA256`, `SHA384`, `SHA512`.

> **miray:** по умолчанию `SHA1`, как в OpenVPN (в mihomo — `SHA256`). С сервером, у которого `auth` не задан, клиент mihomo без явного `auth` не проходит `tls-auth` и не работает с CBC-шифрами. Явное значение не меняется.

## comp-lzo

`yes` или `adaptive` — включить сжатие LZO, как у сервера с `comp-lzo`. Любое другое значение — без сжатия.

## udp

UDP через туннель OpenVPN работает всегда, как и в mihomo; поле ничего не меняет.

## mtu

MTU интерфейса внутри туннеля. По умолчанию 1500.

## ip-stack

IP-стек в пространстве пользователя, через который идёт трафик туннеля.

### ip-stack.mode

`auto` (по умолчанию, это `mips`), `mips` или `gvisor`. `gvisor` — только в сборке с тегом `with_gvisor`.

### ip-stack.congestion-controller

Алгоритм перегрузки TCP у стека `mips`: `cubic`, `reno`, `bbr`, `bbr3`. Не задано — по умолчанию стека.

## dialer-proxy

Подключаться к серверу OpenVPN через другой прокси — как у общего поля в [mihomo](https://wiki.metacubex.one/ru/config/proxies/dialer-proxy/).

## remote-dns-resolve

Разрешать домены через DNS-серверы из [`dns`](#dns) внутри туннеля, а не системным резолвером ядра. Действует, только если `dns` задан.

## dns

DNS-серверы для `remote-dns-resolve`, в формате [`nameserver`](https://wiki.metacubex.one/ru/config/dns/) mihomo.
