# Сборка модуля на Rust {#сборка-модуля-на-rust}

Компонент, собранный с помощью cargo, создаёт нативный модуль Tarantool:
библиотеку Rust-пакета, которую код приложения загружает через `require`
и которая содержит C-функции для `box.schema.func.create`. Тот же
компонент может создавать и программу пакета.
API Tarantool предоставляет
[крейт `tarantool`](https://docs.rs/tarantool/latest/tarantool/).

Backend `cargo` входит в стандартный модуль `build` tt, который есть в
официальном tt и в любом дистрибутиве со стандартными модулями:
команда `tt version --modules` выводит `build`, если он присутствует в tt.

## Создание крейта {#создание-крейта}

В `libs/fastrs/` пакета с именем `myapp`, файл `Cargo.toml`:

```toml
[package]
name = "fastrs"
version = "0.1.0"
edition = "2021"

[dependencies]
tarantool = "11"
```

и `src/lib.rs` с C-функцией `add` и Lua-модулем из одной функции `mul`:

```rust
use std::os::raw::c_int;

use tarantool::proc;
use tarantool::tlua::ffi;

#[proc]
fn add(a: i64, b: i64) -> i64 {
    a + b
}

unsafe extern "C-unwind" fn mul(l: *mut ffi::lua_State) -> c_int {
    let product = ffi::lua_tointeger(l, 1) * ffi::lua_tointeger(l, 2);
    ffi::lua_pushinteger(l, product);
    1
}

/// require('myapp.fastrs')
#[no_mangle]
pub unsafe extern "C" fn luaopen_myapp_fastrs(l: *mut ffi::lua_State) -> c_int {
    ffi::lua_createtable(l, 0, 1);
    ffi::lua_pushcclosure(l, mul, 0);
    ffi::lua_setfield(l, -2, c"mul".as_ptr());
    1
}
```

Задавать `crate-type` в крейте не требуется: tt в любом случае собирает
из него `cdylib`.

## Объявление компонента {#объявление-компонента}

```toml
[components.fastrs]
path = "libs/fastrs"

[components.fastrs.build]
backend = "cargo"
```

```console
$ tt package build
$ find .rocks -name 'fastrs*'
.rocks/lib/tarantool/myapp/fastrs.so
```

Сборка запускает `cargo rustc --lib --crate-type cdylib --profile release`
в каталоге компонента, с `_build/cargo/` проекта в качестве целевого каталога,
и берёт библиотеку из того, о чём сообщает cargo. Модуль называется по имени
библиотеки, `fastrs`; `module = "..."` задаёт другое имя. Это файл
`<module>.so`, а на macOS - `<module>.dylib`, где Tarantool ищет именно
такое имя, и библиотека линкуется там с `-undefined dynamic_lookup`:
символы Tarantool разрешаются при загрузке библиотеки в Tarantool, как и
для модулей на C. `package` выбирает пакет в workspace'е, `features` - его
фичи, а `profile` - профиль сборки, отличный от `release`.

Каталог `target/` компонента, который оставляет cargo, запущенный там
вручную, не раскладывается в дерево пакета: библиотека и процедурные
макросы в нём не являются файлами пакета. Файл `*.lua` компонента
раскладывается так же, как и в любом компоненте.

## Модуль и программа из одного крейта {#модуль-и-программа-из-одного-крейта}

Пакет может содержать как библиотеку, так и бинарный файл: CLI модуля или
процесс, который приложение запускает рядом с Tarantool. Один компонент
собирает и то, и другое, если в его блоке указаны оба. При наличии секции
`[[bin]]` в `Cargo.toml`:

```toml
[[bin]]
name = "fastrs-cli"
path = "src/main.rs"
```

и `src/main.rs`:

```rust
fn main() {
    println!("6 * 7 = {}", 6 * 7);
}
```

блок указывает модуль и бинарный файл:

```toml
[components.fastrs.build]
backend = "cargo"
module = "fastrs"
bin = "fastrs-cli"
```

```console
$ tt package build
$ find .rocks/bin .rocks/lib -name 'fastrs*'
.rocks/bin/fastrs-cli
.rocks/lib/tarantool/myapp/fastrs.so
$ .rocks/bin/fastrs-cli
6 * 7 = 42
```

Сборка сначала выполняет `cargo rustc --lib` для модуля, затем
`cargo build --bin fastrs-cli` и копирует бинарный файл в `.rocks/bin/`,
где его находит описание экземпляра `{binary: fastrs-cli}`; см.
[Запуск внешнего процесса](external-processes.md). Одно лишь поле `module`
собирает только модуль, одно лишь `bin` - только программу, а блок без
обоих собирает библиотеку под её собственным именем, как описано выше.
При `static = true` бинарный файл собирается под musl и не требует
разделяемых библиотек, а модуль собирается как обычно.

## Вызов модуля {#вызов-модуля}

```lua
local fastrs = require('myapp.fastrs')
assert(fastrs.mul(6, 7) == 42)

box.schema.func.create('myapp.fastrs.add', {language = 'C', if_not_exists = true})
assert(box.func['myapp.fastrs.add']:call({20, 22}) == 42)
```

- `require('<namespace>.<module>')` вызывает функцию
  `luaopen_<namespace>_<module>` библиотеки, в которой точки заменены на `_`,
  а часть имени до первого `-` отброшена: `require('my-app.fastrs')` для пакета
  с именем `my-app` вызывает `luaopen_app_fastrs`. При `namespace = ""` модуль
  подключается через `require('fastrs')`, а функция называется `luaopen_fastrs`.
- C-функция называется `<module>.<function>`: Tarantool берёт имя модуля
  из части до последней точки, `myapp.fastrs`, а функцию - из оставшейся
  части, `add`, которую экспортирует `#[proc]`.

Архив `tt package pack` содержит модуль, собранный для той машины, на которой
выполнялась упаковка, и экземпляры установленной версии загружают его точно так
же, как и в проекте.

## Тулчейн {#тулчейн}

`cargo` и `rustc` устанавливаются самостоятельно с помощью
[rustup](https://rustup.rs): tt не устанавливает ни тот, ни другой инструмент
и ищет `cargo` в `PATH`. Крейту `tarantool` требуется rustc версии 1.67.1 или
новее. Модуль линкуется с библиотекой C той машины, на которой он собирается:
собирать его следует на самой старой системе из тех, где он будет запускаться.
