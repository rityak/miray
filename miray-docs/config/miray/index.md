---
title: Блок miray
description: Опции, которых нет в mihomo, — верхнеуровневый блок miray, по подсистемам.
lang: ru
order: 1
section: miray
status: miray
since: v1.19.33-miray.2
---

# Блок `miray`

Опции miray, которых нет в mihomo. Сгруппированы по подсистемам, как остальной конфиг: `miray.dns`, `miray.proxy-groups`, дальше — другие. Конфиг без блока `miray:` работает с умолчаниями, указанными у каждой опции; mihomo блок игнорирует.

```yaml
miray:
  dns:
    unspecified-as-block: true
  proxy-groups:
    relative-tolerance: 10
```

| Подсистема | Страница |
|---|---|
| `dns` | [miray.dns](dns.md) |
| `proxy-groups` | [miray.proxy-groups](proxy-groups.md) |

Текущие значения видны в `GET /configs` в поле `miray`.
