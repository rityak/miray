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
| `tun` | `mtu` на Linux по умолчанию 65535 (в mihomo 9000) с откатом на 9000; `gso` по умолчанию включён на Linux у стека `mips`; отказ дозвона и `REJECT` — RST на SYN (`mips`, `gvisor`) или сброс соединения, а не пустой ответ; для UDP — ICMP «порт недоступен» (`mips`, `gvisor`) | [TUN](config/inbound/tun.md) |
| `rules`: `REJECT` | TCP-соединение закрывается сбросом (RST), а не штатно: приложение видит отказ, а не пустой ответ; через TUN `mips`/`gvisor` — отказ на SYN, на UDP — ICMP «порт недоступен» первой датаграмме сессии | [TUN](config/inbound/tun.md) |
| UDP-сессии | Сессия без трафика живёт 5 мин (в mihomo 60 с), к портам DNS, NTP, STUN — 10 с; ICMP от сервера прокси не закрывает сессию ss | [miray.udp](config/miray/udp.md) |
| `proxy-groups`: `url-test`, `fallback`, `load-balance` | Соединение не ждёт таймаута узла: после задержки попытки (250 мс … 2 с) параллельно пробуется следующий узел группы (RFC 8305), отказавший узел откладывается до проверки здоровья, не дольше 30 с; проигрыш гонки — не отказ | [общие поля](config/proxy-groups/index.md), [url-test](config/proxy-groups/url-test.md), [fallback](config/proxy-groups/fallback.md), [load-balance](config/proxy-groups/load-balance.md) |
| `proxy-groups`: `url-test`, `tolerance` | Допуск — большее из `tolerance` и 10% задержки самого быстрого узла (`miray.proxy-groups.relative-tolerance`); держит и первый в списке узел | [url-test](config/proxy-groups/url-test.md#tolerance), [miray.proxy-groups](config/miray/proxy-groups.md) |
| Общие настройки: `tcp-concurrent`; `proxies`: `ip-version` | Адреса перебираются по RFC 8305: со сдвигом 250 мс, IPv6 первым (при `ipv4-prefer` — IPv4); `tcp-concurrent` не влияет. В mihomo IPv4 и IPv6 — одновременно, адреса семейства — по очереди | [общие настройки](config/general.md#tcp-concurrent), [прокси](config/proxies/index.md#ip-version) |
| `proxies`: `openvpn` | Без `auth` — SHA1, как в OpenVPN (в mihomo — SHA256) | [openvpn](config/proxies/openvpn.md) |
| `proxies`: `trusttunnel` | Пока потоки передают данные, `_check` раз в 30 с без `health-check`; по QUIC — PING раз в 10 с | [trusttunnel](config/proxies/trusttunnel.md) |
| `listeners`: `hysteria2`, `tuic` | На остановке и reload соединения закрываются с CONNECTION_CLOSE; на остановке закрываются все `listeners:`, не только TUN | [listeners](config/inbound/listeners/index.md), [hysteria2](config/inbound/listeners/hysteria2.md), [tuic](config/inbound/listeners/tuic.md) |
| `tls.custom-certifactes` | Действуют и для прокси, которые собирают TLS при создании (hysteria2, tuic, HTTPS, TLS-SOCKS5) | [TLS](config/tls.md) (в вики mihomo блок не описан) |
| DNS: адрес `0.0.0.0`/`::` в ответе | Блокировка: отказ сразу, без повторов и без сокета | [miray.dns](config/miray/dns.md) |
| Reload (`PUT /configs`) | providers грузятся, пока работает старый конфиг; кеш DNS переживает reload без изменений `dns:`; открытые UDP-сессии не рвутся; новое TCP-соединение ждёт конца reload до 5 с; `GET /configs` отдаёт блок `miray` | [API](api/index.md) |

## Переменные окружения miray

Выключатели отдельных оптимизаций, для диагностики; в конфиге их нет.

| Переменная | Что выключает |
|---|---|
| `MIRAY_QUIC_GRO=off` | приём QUIC через UDP GRO |
| `MIRAY_SALAMANDER_OOB=off` | пакетную отправку и приём hysteria2 с Salamander |
| `MIRAY_DIAG=off` | журнал диагностической сборки (тег `miraydiag`) |
