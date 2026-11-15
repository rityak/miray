---
title: quri
description: Клиентский quri outbound с HTTP/3 и резервным HTTP/2.
lang: ru
order: 26
section: proxies
status: miray
since: unreleased
---

# quri

Самостоятельный транспорт первого плеча до quri relay. Правила miray выбирают
нужный профиль на клиенте. Relay использует один неизменяемый выход на сеанс.
Серверный процесс quri и его внешний сайт запускаются отдельно от ядра miray.

```yaml
proxies:
  - name: relay-vless
    type: quri
    server: relay.example
    port: 443
    sni: relay.example
    token: REPLACE_WITH_AT_LEAST_32_RANDOM_BYTES
    transports: [h3, h2]
    upstream: "vless://UUID@exit.example:443?security=reality&type=tcp&sni=site.example&pbk=PUBLIC_KEY&sid=SHORT_ID&fp=chrome"
```

`upstream` передаётся relay внутри защищённого control-запроса. Для выбора
выхода клиентом сервер должен явно разрешать `allow_client_upstream`.
Без `upstream` используется выход, настроенный оператором relay, либо direct.
Второй профиль с HY2 на том же relay создаёт отдельную quri-сессию.

| Поле | Значение |
|---|---|
| `name`, `type` | Имя профиля и `quri` |
| `server`, `port` | Адрес relay и TCP/UDP порт |
| `token` | Индивидуальный секрет длиной 32–256 байт |
| `sni` | Имя для проверки сертификата; по умолчанию `server` |
| `transports` | Разрешённые `h3`, `h2`; по умолчанию оба |
| `ports` | Дополнительные порты для установления соединения, до 16 |
| `upstream` | Ссылка VLESS, HY2 или следующего quri; требует разрешения relay |
| `ca` | Дополнительный доверенный CA: PEM или путь к файлу |
| `profile` | Полная секретная ссылка `quri://`; заменяет поля подключения |

Поддерживаются общие `interface-name`, `routing-mark`, `dialer-proxy`,
`ip-version`. Сертификат relay всегда проверяется. Отключение TLS verification,
REALITY первого плеча, uTLS/Chrome fingerprint пока не реализованы.

Ссылки `quri://` принимаются конвертером подписок. Ссылки содержат секрет и
не должны публиковаться или попадать в логи.

В h3 UDP ненадёжный, без фрагментации; по умолчанию payload не больше 1150 байт.
В h2 UDP инкапсулируется в надёжные упорядоченные capsules и зависит от потерь
TCP. При реконнекте UDP-ассоциации восстанавливаются, TCP-потоки закрываются.
`ports` пока не означает смену порта живого соединения без обрыва.
