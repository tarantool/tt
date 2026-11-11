# Сборка модуля на Rust

Компонент-модуль, собранный с помощью cargo, - это нативный модуль
Tarantool: библиотека Rust-пакета, которую код приложения загружает через
`require` и которая содержит C-функции для `box.schema.func.create`.
API Tarantool предоставляет [крейт `tarantool`](https://docs.rs/tarantool/latest/tarantool/).

## Пишем крейт {#write-the-crate}

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

Задавать `crate-type` в крейте не требуется: tt в любом случае собирает из него `cdylib`.

## Объявляем компонент {#declare-the-component}

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
в каталоге компонента, с `_build/cargo/` проекта в качестве каталога target,
и берёт библиотеку из того, о чём сообщает cargo. Модуль называется по имени
библиотеки, `fastrs`; `module = "..."` задаёт другое имя. Это файл
`<module>.so`, а на
macOS - `<module>.dylib`, где Tarantool ищет именно такое имя. Библиотека
там линкуется с `-undefined dynamic_lookup`: символы Tarantool разрешаются
при загрузке библиотеки в Tarantool, как и для модулей на C. `package`
выбирает пакет в workspace, `features` - его feature-флаги, а `profile` -
профиль сборки, отличный от `release`.

Каталог `target/` компонента, который оставляет cargo, запущенный там
вручную, не раскладывается в дерево пакета: библиотека и процедурные
макросы в нём файлами пакета не являются.
Файлы `*.lua` компонента раскладываются так же, как и в любом другом
компоненте-модуле.

## Вызов модуля {#call-it}

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
  подключается как `require('fastrs')`, а функция называется `luaopen_fastrs`.
- C-функция именуется как `<module>.<function>`: Tarantool берёт имя модуля
  из части до последней точки (`myapp.fastrs`), а имя функции - из остальной
  части (`add`), которую экспортирует `#[proc]`.

Архив `tt package pack` содержит модуль, собранный под ту платформу, на
которой выполняется упаковка, а инстансы установленной версии загружают его
точно так же, как в проекте.

## Тулчейн {#the-toolchain}

`cargo` и `rustc` устанавливаются самостоятельно с помощью [rustup](https://rustup.rs):
tt не устанавливает ни тот, ни другой инструмент и ищет `cargo` в `PATH`.
Крейту `tarantool` требуется rustc 1.67.1 или новее. Модуль линкуется с
библиотекой C той системы, на которой он собирается: собирать его следует на
самой старой из поддерживаемых систем.
