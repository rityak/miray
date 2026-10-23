# miray

Форк [mihomo](https://github.com/MetaCubeX/mihomo). Рабочая ветка — `dev`.

## Что изменено в ядре

Замеры и обоснования — во внутренней документации проекта, здесь только сами изменения.

### TUN
- `gso` включён по умолчанию для стеков `mips` и `system` на Linux; если TUN с ним не открылся — запуск без него.
- Стек `system` на Linux по умолчанию с MTU 65535, как в sing-box, с откатом на 9000; явный `mtu:` не трогается.
- gvisor: буфер TCP 1 МиБ с ростом до 8/6 МиБ вместо 20 КиБ.
- mipstack: буфер приёма TCP растёт и на отдаче.
- Стек `system` отбрасывает битые пакеты, а не падает.

### UDP: общий путь
- Пакеты уже открытых UDP-потоков TUN идут в сессию напрямую, мимо общей очереди и NAT-таблицы.
- Входящий UDP листенеров и ответы исходящих сессий читаются пачками (`recvmmsg`).
- DIRECT отправляет пачками (`sendmmsg`) и через UDP GSO.
- Ответы сессии, уже прочитанные пачкой, уходят клиенту тоже пачкой — и в TUN, и в `tunnels:`.
- Чтение ответов без горутины, `select` и лишних аллокаций на каждую датаграмму; адрес источника кешируется на сессию.
- Открытые UDP-сессии переживают reload конфига без потерь.

### Shadowsocks
- Клиент ходит на сервер через подключённый UDP-сокет и читает ответы пачками своим `recvmmsg`.
- Клиент и сервер шифруют и отправляют UDP пачками (`sendmmsg` / UDP GSO).

### QUIC: hysteria2 и tuic
- BBR не переполняет очередь канала периодическими пробами: нет всплесков задержки под загрузкой.
- Отправка GSO-пачками, приём через UDP GRO (`MIRAY_QUIC_GRO=off` выключает), на Windows — USO.
- Очередь входящих пакетов 4096 вместо 256.
- Соединение переживает переполненный буфер сокета и смену сети; GREASE QUIC bit.
- Датаграммы одного потока не переставляются.
- hysteria2: дешевле путь UDP; Salamander снова шлёт и принимает пачками, XOR по словам (`MIRAY_SALAMANDER_OOB=off` выключает).
- hysteria2-сервер отвечает на TCP-запрос сразу после соединения с целью, как sing-box: официальный клиент hysteria ждёт этого ответа, прежде чем слать данные.
- hysteria2-клиент, закрывая TCP-поток до ответа сервера, закрывает только отправку, как официальный клиент: сервер успевает ответить и доставить загрузку.
- tuic: датаграммы не забивают остальные данные QUIC, аутентификация не ждёт их очередь, TCP и UDP идут по одному соединению; большие датаграммы не режутся на две, если путь их пропускает.
- После рестарта сервера QUIC-клиенты (hysteria2, tuic, trusttunnel по QUIC, masque) переподключаются сразу, а не через idle-таймаут: на Linux сокет клиента получает ICMP «порт закрыт» (`IP_RECVERR`).

### Потоки и TLS
- VLESS XHTTP (все режимы, в том числе `packet-up` с сервером Xray) и trusttunnel не теряют хвост данных при закрытии соединения.
- XTLS Vision не теряет последние байты, пришедшие вместе с закрытием TLS.
- utls и REALITY: все TLS-записи одного `Write` уходят одним системным вызовом.
- smux: запись, брошенная по таймауту, не уходит в сеть с чужими данными.
- Исправлены гонки данных в XTLS Vision, restls и anytls.
- `tls.custom-certifactes` действуют и для прокси, которые собирают TLS при создании (hysteria2, tuic, HTTPS- и TLS-SOCKS5-прокси): сертификаты загружаются до разбора прокси. Раньше такой сервер проходил только с `skip-cert-verify` или отпечатком.

### TCP, DNS, reload
- Reload без простоя: providers грузятся, пока работает старый конфиг; новое соединение ждёт конца reload, а не сбрасывается.
- Кеш DNS и соединения к DNS-серверам переживают reload, если `dns:` не менялся; DNS `tcp://` держит соединения открытыми; медленный DNS не блокирует остальной трафик.
- Нет 200 мс ожидания early-data для серверов, которые говорят первыми (SSH, SMTP); при переборе адресов каждый получает свою долю таймаута.

### OpenVPN
- Без `auth` в конфиге клиент берёт SHA1, как сам OpenVPN: tls-auth и шифры без AEAD работают с сервером, у которого `auth` тоже не задан. Явный `auth` работает как раньше.

### Прочее
- Апдейтер не ставит mihomo поверх miray.

## Репозитории

- [rityak/miray](https://github.com/rityak/miray) — ядро
- [rityak/quic-go](https://github.com/rityak/quic-go) — QUIC для hysteria2 и tuic
- [rityak/sing-quic](https://github.com/rityak/sing-quic) — hysteria2
- [rityak/sing-tun](https://github.com/rityak/sing-tun) — TUN
- [rityak/mipstack](https://github.com/rityak/mipstack) — сетевой стек mips
- [rityak/utls](https://github.com/rityak/utls) — TLS-клиент
- [rityak/smux](https://github.com/rityak/smux) — мультиплексор
- [rityak/restls-client-go](https://github.com/rityak/restls-client-go) — restls
- [rityak/sing-shadowsocks](https://github.com/rityak/sing-shadowsocks) — сервер Shadowsocks
- [rityak/sing-shadowsocks2](https://github.com/rityak/sing-shadowsocks2) — клиент Shadowsocks

В форках наш код лежит в ветке `miray`.
