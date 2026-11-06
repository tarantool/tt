# Известные проблемы

Здесь собраны типичные ошибки и частые проблемы, о которых сообщают
пользователи. Самый актуальный список проблем - на странице
[GitHub Issues](https://github.com/tarantool/tt/issues).

## Содержание {#contents}

- [Не включайте box.cfg.background](#do-not-set-the-boxcfgbackground)

## Не включайте box.cfg.background {#do-not-set-the-boxcfgbackground}

`tt instance start` сам демонизирует процесс Tarantool. Параметр
[box.cfg.background][cfg-background] делает то же самое изнутри процесса
Tarantool. Эти механизмы конфликтуют друг с другом: в результате
`tt instance status` показывает неверный статус инстанса, а остановить его
командой `tt instance stop` не удаётся.

В коде приложений, которые запускаются через `tt`, параметр
`box.cfg.background = true` включать нельзя.

[cfg-background]: https://www.tarantool.io/ru/doc/latest/reference/configuration/#confval-background
