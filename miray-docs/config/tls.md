---
title: TLS
description: Блок tls — сертификат контроллера и свои доверенные сертификаты; в miray они действуют и для прокси, которые собирают TLS при создании.
lang: ru
order: 5
section: general
status: changed
since: v1.19.33-miray.1
---

# TLS

Верхнеуровневый блок `tls`: сертификат, с которым ядро само принимает TLS, и сертификаты, которым ядро доверяет как клиент. В вики mihomo блок не описан.

```yaml
tls:
  certificate: ./server.crt
  private-key: ./server.key
  # client-auth-type: require-and-verify
  # client-auth-cert: ./clients.crt
  # ech-key: |
  #   -----BEGIN ECH KEYS-----
  #   ...
  custom-certifactes:
    - |
      -----BEGIN CERTIFICATE-----
      ...
      -----END CERTIFICATE-----
```

## certificate / private-key

Сертификат и закрытый ключ (PEM строкой или путь к файлу) для HTTPS-контроллера `external-controller-tls`. Без них HTTPS-контроллер не запускается (ошибка в журнале).

## client-auth-type

Проверка сертификата клиента контроллера: `request`, `require-any`, `verify-if-given`, `require-and-verify`. Не задано — не запрашивать; но если задан `client-auth-cert`, то `require-and-verify`.

## client-auth-cert

Сертификаты, которыми проверяется клиент при `client-auth-type` (PEM или путь).

## ech-key

Ключ ECH для HTTPS-контроллера.

## custom-certifactes

Название — с опечаткой, как в mihomo. Список сертификатов (PEM текстом), которым ядро доверяет в дополнение к системным и встроенным: проверка сертификата у прокси, DNS по TLS/HTTPS/QUIC, загрузки провайдеров. Нужен, когда сервер подписан своим центром сертификации. Сертификат, который не разобрался, пропускается с предупреждением в журнале.

Системные сертификаты выключаются переменной окружения `DISABLE_SYSTEM_CA=1`, встроенные — `DISABLE_EMBED_CA=1`.

> **miray:** сертификаты добавляются до создания прокси, поэтому действуют для всех прокси. В mihomo hysteria2, tuic, HTTP и SOCKS5 поверх TLS собирают пул доверенных сертификатов при создании — раньше, чем ядро добавит `custom-certifactes`. Сервер, которому доверяют только через этот список, у них не проходит проверку, хотя у trojan проходит.
