---
title: Карта отличий от mihomo
description: По каждому блоку конфига — что в miray работает иначе и где это описано.
lang: ru
order: 1
---

# Карта отличий от mihomo

Блоки конфига, у которых в miray другое умолчание или другое поведение при тех же значениях. Блок со страницей miray описан здесь полностью; блок без неё пока описан только этой строкой, остальное — как в [вики mihomo](https://wiki.metacubex.one/ru/config/). Чего нет в таблице, работает как в mihomo.

| Блок | Что иначе в miray | Страница |
|---|---|---|
| `miray` | Блок есть только в miray: опции по подсистемам | [miray](config/miray/index.md) |
| `tun` | `mtu` на Linux по умолчанию 65535 (в mihomo 9000) с откатом на 9000; `gso` по умолчанию включён на Linux у стека `mips` | [TUN](config/inbound/tun.md) |
| `proxy-groups`: `url-test`, `fallback`, `load-balance` | Соединение не ждёт таймаута узла: после задержки попытки (250 мс … 2 с) параллельно пробуется следующий узел группы (RFC 8305), отказавший узел откладывается до проверки здоровья, не дольше 30 с; проигрыш гонки — не отказ | ещё нет — [mihomo](https://wiki.metacubex.one/ru/config/proxy-groups/) |
| `proxies`: `openvpn` | Без `auth` — SHA1, как в OpenVPN (в mihomo — SHA256) | ещё нет — [mihomo](https://wiki.metacubex.one/ru/config/proxies/) |
| `proxies`: `trusttunnel` | Пока потоки передают данные, `_check` раз в 30 с без `health-check`; по QUIC — PING раз в 10 с | ещё нет — [mihomo](https://wiki.metacubex.one/ru/config/proxies/) |
| `listeners`: `hysteria2`, `tuic` | На остановке и reload соединения закрываются с CONNECTION_CLOSE; на остановке закрываются все `listeners:`, не только TUN | ещё нет — [mihomo](https://wiki.metacubex.one/ru/config/inbound/listeners/) |
| `tls.custom-certifactes` | Действуют и для прокси, которые собирают TLS при создании (hysteria2, tuic, HTTPS, TLS-SOCKS5) | ещё нет; в вики mihomo блок `tls` не описан |
| DNS: адрес `0.0.0.0`/`::` в ответе | Блокировка: отказ сразу, без повторов и без сокета | [miray.dns](config/miray/dns.md) |
| Reload (`PUT /configs`) | providers грузятся, пока работает старый конфиг; кеш DNS переживает reload без изменений `dns:`; открытые UDP-сессии не рвутся | ещё нет — [mihomo](https://wiki.metacubex.one/ru/api/) |

## Переменные окружения miray

Выключатели отдельных оптимизаций, для диагностики; в конфиге их нет.

| Переменная | Что выключает |
|---|---|
| `MIRAY_QUIC_GRO=off` | приём QUIC через UDP GRO |
| `MIRAY_SALAMANDER_OOB=off` | пакетную отправку и приём hysteria2 с Salamander |
| `MIRAY_DIAG=off` | журнал диагностической сборки (тег `miraydiag`) |
