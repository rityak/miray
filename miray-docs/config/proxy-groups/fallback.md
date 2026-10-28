---
title: fallback
description: Группа, которая берёт первый живой узел по порядку; в miray — с переключением при отказе узла.
lang: ru
order: 33
section: proxy-groups
mihomo: https://wiki.metacubex.one/ru/config/proxy-groups/fallback/
status: changed
since: v1.19.33-miray.1
---

# fallback

Берёт первый по порядку `proxies` (затем `use`) узел, который жив по проверке здоровья. Если узел выбран вручную в панели, берётся он, пока жив.

```yaml
proxy-groups:
  - name: failover
    type: fallback
    proxies: [primary, backup]
    url: https://www.gstatic.com/generate_204
    interval: 300
```

Своих полей нет — только [общие](index.md).

> **miray:** если выбранный узел не подключился вовремя или вернул ошибку, соединение уходит на следующий живой узел по порядку, не дожидаясь таймаута и проверки здоровья; отказавший узел откладывается до проверки (не дольше 30 с). В mihomo соединение через упавший узел получает ошибку. Подробно — [переключение при отказе узла](index.md#переключение-при-отказе-узла).
