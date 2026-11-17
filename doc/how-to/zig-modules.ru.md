# Сборка модуля на Zig {#build-a-module-in-zig}

Компонент, собранный с помощью zig, создаёт нативный модуль Tarantool:
библиотеку, которую код приложения загружает через `require` и которая
содержит C-функции для `box.schema.func.create`. Тот же компонент может
создавать и программу из того же `build.zig`.
[zig-tarantool](https://github.com/tarantool-contrib/zig-tarantool)
предоставляет коду на Zig API Tarantool и вспомогательные функции для
`build.zig`, собирающие модуль и программу.

Backend `zig` входит в число стандартных модулей `build` tt, которые есть в
официальном tt и в любом дистрибутиве со стандартными модулями:
`tt version --modules` выводит `build`, если он присутствует в tt.

## Установка zig {#get-zig}

zig устанавливается самостоятельно: tt его не устанавливает, а ищет `zig`
в `PATH`, проверяя сначала `.tarantool/bin` проекта. Для zig-tarantool и
приведённых здесь примеров требуется zig 0.17.1:

```console
$ zig version
0.17.1
```

Backend собирает и с zig 0.16, если `build.zig` не использует
zig-tarantool.

## Написание пакета {#write-the-package}

В `libs/hello/` пакета с именем `myapp` файл `build.zig.zon` задаёт имя
пакета, пока без зависимостей:

```zig
.{
    .name = .hello,
    .version = "0.1.0",
    .fingerprint = 0x3610a6861c561281,
    .minimum_zig_version = "0.17.1",
    .dependencies = .{},
    .paths = .{ "build.zig", "build.zig.zon", "src" },
}
```

Fingerprint взят для нового пакета: zig сам сообщает, что подставить,
если он отсутствует, `error: missing top-level 'fingerprint' field; suggested value:
0x...`.

`build.zig` собирает модуль `hello` и программу `hello-client`:

```zig
const std = @import("std");
const tarantool = @import("tarantool");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    // zig-out/lib/hello.so, hello.dylib on macOS.
    const hello = tarantool.addModule(b, .{
        .name = "hello",
        .root_source_file = b.path("src/hello.zig"),
        .target = target,
        .optimize = optimize,
    });
    tarantool.installModule(b, hello);

    // zig-out/bin/hello-client.
    const client = tarantool.addExecutable(b, .{
        .name = "hello-client",
        .root_source_file = b.path("src/client.zig"),
        .target = target,
        .optimize = optimize,
    });
    b.installArtifact(client);
}
```

`addModule` создаёт библиотеку, символы Tarantool в которой разрешаются при
загрузке в Tarantool, а `installModule` устанавливает её как `lib/hello.so`
- имя, под которым её загружает Tarantool (`lib/hello.dylib` на macOS).

`src/hello.zig` с C-функцией `add` и Lua-модулем из одной функции `mul`:

```zig
const t = @import("tarantool");

/// The C function myapp.hello.add.
fn add(a: i64, b: i64) i64 {
    return a + b;
}

/// mul of the Lua module, require('myapp.hello').
fn mul(a: i64, b: i64) i64 {
    return a * b;
}

comptime {
    t.proc.exportProc("add", add);
    t.lua.exportModule("myapp_hello", .{ .mul = mul });
}
```

и `src/client.zig` - программа, вызывающая `add` по сети:

```zig
const std = @import("std");
const client = @import("tarantool-client");

pub fn main(init: std.process.Init) !void {
    const address = init.environ_map.get("TT_URI") orelse "127.0.0.1:3301";
    var conn = try client.Connection.connect(init.gpa, init.io, .{
        .address = address,
        .user = "guest",
        .password = "",
    });
    defer conn.close();

    const sum = try conn.call("myapp.hello.add", .{ 20, 22 }, i64);
    std.debug.print("20 + 22 = {d}\n", .{sum});
}
```

Затем в `libs/hello/` команда `zig fetch --save` добавляет zig-tarantool в
`build.zig.zon` как зависимость `tarantool`, зафиксированную по коммиту
своими `.url` и `.hash`:

```console
$ cd libs/hello
$ zig fetch --save=tarantool git+https://github.com/tarantool-contrib/zig-tarantool#c43e6c84c371b59e89cdc0354b8d377ffdd93dc8
$ grep -A3 '\.tarantool' build.zig.zon
        .tarantool = .{
            .url = "git+https://github.com/tarantool-contrib/zig-tarantool#c43e6c84c371b59e89cdc0354b8d377ffdd93dc8",
            .hash = "tarantool-0.0.0-LczyxTwvEQC19W4vsXUbauN_kO8EABpFyamoek3lPuqV",
        },
$ cd ../..
```

`zig fetch --save` ищет `build.zig` пакета, поэтому выполняется после его
создания, а zig 0.17 сохраняет скачанное в `zig-pkg/` рядом с `build.zig.zon`.
Этот каталог не входит в состав файлов пакета: tt не включает его в структуру
каталогов, а при сборке скачивание происходит в отдельный каталог вне исходного
кода (см. ниже). `zig-pkg/` и `zig-out/` добавляют в `.gitignore`.

## Объявление компонента {#declare-the-component}

Блок указывает модуль и программу, создаваемые сборкой:

```toml
[components.hello]
path = "libs/hello"

[components.hello.build]
backend = "zig"
module = "hello"
bin = "hello-client"
```

```console
$ tt package build
$ find .rocks/bin .rocks/lib -name 'hello*'
.rocks/bin/hello-client
.rocks/lib/tarantool/myapp/hello.so
$ find .rocks -path '*zig-pkg*' | wc -l
0
```

Сборка запускает в каталоге компонента команду

```console
zig build -p _build/component/hello/zig-out --cache-dir _build/component/hello/zig-cache --pkg-dir _build/component/hello/zig-pkg --release=safe
```

которая выполняет установку в собственный префикс сборки вне исходного кода и
сохраняет там же кэш проекта и скачиваемых им пакетов; `tt -V package build`
показывает эту строку с абсолютными путями. `--pkg-dir` передаётся в zig
0.17 и новее, кроме случаев, когда задана переменная `ZIG_LOCAL_PKG_DIR`,
каталог пакетов из которой zig берёт в таком случае, независимо от того,
экспортирована ли она в окружении запуска tt или задана в `env` блока: `env =
{ ZIG_LOCAL_PKG_DIR = "/home/me/.cache/zig-pkg" }` позволяет использовать
один каталог между проектами. zig 0.16 хранит пакеты в своём глобальном кэше
и этот флаг не получает. tt берёт модуль из каталога `lib/` префикса:
`hello.so` или `hello.dylib`, как его устанавливает `installModule`, либо
`libhello.so` или `libhello.dylib`, как устанавливает библиотеку
`b.installArtifact`, и копирует его как `<module>.so` (`<module>.dylib` на
macOS) туда, где Tarantool ищет это имя. Программа копируется из каталога
`bin/` префикса в `.rocks/bin/`, где её находит описание экземпляра
`{binary: hello-client}`; см.
[Запуск внешнего процесса](external-processes.md). Задание только `module`
собирает только модуль, только `bin` - только программу, а блок без обоих
полей считается ошибкой. Модуль с точками, `module = "codec.fast"`,
соответствует файлу `lib/codec/fast.so` префикса, как устанавливает модуль
с таким именем `installModule`, и `codec/fast.so` в пространстве имён
компонента.

Если модуль или программа не были установлены сборкой, сборка завершается с
ошибкой и выводит список того, что было установлено:

```console
$ tt package build
   ⨯ building component "hello": zig build installed no module "hullo": .../_build/component/hello/zig-out/lib has none of hullo.so, hullo.dylib, libhullo.so, libhullo.dylib (it installed hello.so); install it with installModule of zig-tarantool, or name the step that installs it in steps
```

Каталог `zig-out/` компонента, остающийся после запуска `zig build` вручную, не
попадает в структуру каталогов; то же относится к `zig-pkg/`, остающемуся после
ручных `zig fetch --save` и `zig build`, а также к исходным файлам `.zig`: файл
`*.lua` компонента попадает в структуру каталогов, как и в любом компоненте, но
файл из `zig-pkg/` (например, `examples/kv/init.lua` из zig-tarantool) в неё не
попадает. Переименование модуля в блоке (`module = "greet"` вместо `hello`) не
оставляет старый `hello.so`: сборка блока с изменениями собирает компонент
заново.

Остальные поля блока:

- `steps` - шаги запускаемого `zig build`, по умолчанию его стандартный шаг
  `install`: если `build.zig` устанавливает модуль из другого шага, его
  указывают явно, `steps = ["modules"]`.
- `optimize` - режим, в котором выполняется сборка, по умолчанию
  `ReleaseSafe`: `Debug`, `ReleaseSafe`, `ReleaseFast` или `ReleaseSmall`.
  tt передаёт его как `--release=safe`, `fast` или `small`, а `Debug` - без
  флага, что `build.zig` принимает независимо от объявления `-Doptimize`;
  если `standardOptimizeOption` получает режим, сборка собирается в нём.
- `options` - параметры, объявляемые `build.zig` через `b.option`,
  передаваемые в виде `-D<name>=<value>`, каждый в виде строки:
  `options = { tls = "true" }` для `b.option(bool, "tls", ...)`.
  `optimize` сюда не входит, поскольку задаётся отдельным полем.
- `incremental = true` пропускает сборку, если её блок, файлы компонента,
  версия zig, а также `build.zig` и `build.zig.zon` в текущем каталоге не
  менялись с прошлой сборки. Изменения зависимости по пути вне компонента не
  отслеживаются: флаг `--rebuild` форсирует пересборку в любом случае.
- `static = true` проверяет, что программа не зависит от разделяемых
  библиотек, а модуль зависит только от библиотек C. Блок, собирающий только
  программу, собирает её под `<arch>-linux-musl`, `-Dtarget`, когда
  `build.zig` принимает `-Dtarget`, а в `options` target не указан: программа,
  линкующая библиотеку C, линкует musl статически. Блок с модулем и программой
  собирает обе цели под целевую платформу модуля. Программа из
  `addExecutable` не линкует библиотеку C и остаётся статической изначально.

## Вызов {#call-it}

`app.lua` в проекте:

```lua
local hello = require('myapp.hello')
assert(hello.mul(6, 7) == 42)

box.cfg({listen = '127.0.0.1:3301'})
box.schema.func.create('myapp.hello.add', {language = 'C', if_not_exists = true})
box.schema.user.grant('guest', 'execute', 'function', 'myapp.hello.add',
    {if_not_exists = true})
assert(box.func['myapp.hello.add']:call({20, 22}) == 42)
```

Скрипт запускают в одном терминале, где Tarantool продолжает обслуживать
запросы после выполнения скрипта, а программу - в другом:

```console
$ tt run app.lua
```

```console
$ .rocks/bin/hello-client
20 + 22 = 42
```

- `require('<namespace>.<module>')` вызывает функцию
  `luaopen_<namespace>_<module>` библиотеки, где точки заменяются на `_`:
  модуль, который `exportModule` экспортирует как `myapp_hello`. При
  `namespace = ""` модуль загружается как `require('hello')` и экспортируется
  как `hello`.
- C-функция именуется как `<module>.<function>`: Tarantool берёт имя модуля
  из части до последней точки, `myapp.hello`, а имя функции - из остальной
  части, `add`, которую экспортирует `exportProc`.

Архив `tt package pack` содержит модуль и программу, собранные под
платформу машины, на которой упаковывался пакет, а экземпляры установленной
версии загружают и запускают их так же, как в проекте.

## Тулчейн {#the-toolchain}

zig сам скачивает зависимости из `build.zig.zon` при сборке в свой глобальный
кэш, а zig 0.17 копирует их оттуда в
`_build/component/<component>/zig-pkg/`: `tt package fetch` сборку не
запускает, а `tt package build --locked` сохраняет блокировку tt, не отключая
zig от сети. Хеши в `build.zig.zon` фиксируют состав скачиваемого zig. Модуль
линкуется с библиотекой C той машины, на которой собирается: собирать его
следует на наиболее старой системе из тех, где он будет запускаться.
