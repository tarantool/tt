# Написание backend'а сборки {#write-a-build-backend}

Backend сборки собирает компоненты пакета. Его указывает блок сборки
компонента:

```toml
[components.native.build]
backend = "cargo"
module = "fast"
```

В ядре tt собственных backend'ов нет. Каждый backend поступает из модуля tt -
модуля Go, скомпилированного в [дистрибутив tt](build-distribution.md).
Собственные backend'ы tt - `make`, `shell`, `c`, `lua-c`, `cargo`, `cmake`,
`go` и `zig` - составляют стандартный модуль `build`, расположенный в
`modules/build` в репозитории tt. Они написаны по тому же контракту, что и
backend любого другого модуля, `github.com/tarantool/tt/sdk/build`, и служат
примерами backend'ов каждого вида.

На этой странице с нуля пишется backend `template`. С помощью `envsubst` он
рендерит шаблон компонента в файл данных пакета. Затем мы протестируем его,
соберём в состав tt и соберём с его помощью пакет. Устройство модуля - его
конструктор, команды и дистрибутив - описано в руководстве
[Написание модуля tt](write-a-module.md).

SDK находится в статусе alpha: его API может измениться в любом релизе tt.

## Настройка модуля Go {#set-up-the-go-module}

```sh
mkdir tt-template && cd tt-template
go mod init example.com/tt-template
go get github.com/tarantool/tt/sdk@<commit> \
    github.com/tarantool/tt/sdk/build@<commit> \
    github.com/tarantool/tt/sdk/connect@<commit>
go get github.com/stretchr/testify
```

Подключите модули SDK по одному коммиту tt, как описано в
[Написание модуля tt](write-a-module.md#set-up-the-go-module). Модуля
`sdk/build` пока нет в релизах: в `v3.0.0-alpha.2` его ещё нет, поэтому берём
более поздний коммит ядра. Укажите также и `sdk/connect`, даже если модуль его
не импортирует: `go.mod` модуля `sdk` требует его на версии-плейсхолдере,
которую разрешает только репозиторий самого tt.

Чтобы вместо этого собрать модуль на рабочем дереве tt с ещё не отправленными
в репозиторий изменениями SDK, запускают `tt binary workspace` в каталоге
дистрибутива, описанном ниже, поле `[core].path` которого указывает на это
дерево: команда создаёт `go.work`, благодаря которому модуль собирается на SDK
из рабочего дерева независимо от требований его `go.mod`, а
`tt binary workspace --remove` удаляет его, когда изменения отправлены. В
разделе [Совместная разработка с ядром](write-a-module.md#develop-together-with-the-core)
показан этот цикл и то, как подключить модуль SDK, которого ещё нет ни в одном
отправленном коммите. Команда `go mod tidy` игнорирует `go.work`, поэтому ей
нужны требования из `go get` выше. Сборка дистрибутива также игнорирует
`go.work` и собирает модуль на SDK своего ядра, что бы ни требовал `go.mod`
модуля.

## Написание backend'а {#write-the-backend}

`backend.go`:

```go
// Package template is a tt module with one build backend, template: it
// renders a template of a component with envsubst into a data file of the
// package.
package template

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/build"
)

// Name is the name of the backend: backend = "template" in a build block.
const Name = "template"

// tool is the program the backend runs.
const tool = "envsubst"

// Config are the fields of a template build block.
type Config struct {
	// Template is the template, relative to the working directory of the
	// build. Its name ends with .in. Required.
	Template string `toml:"template"`
	// Vars are the variables the template is rendered with.
	Vars map[string]string `toml:"vars"`
}

// Backend returns the template backend.
func Backend() build.Backend {
	return build.Backend{
		Name:        Name,
		Produces:    []build.Result{build.Data},
		Config:      func() any { return new(Config) },
		New:         build.Typed(newBuild),
		Incremental: true,
		OutputFrom:  build.OutputNone,
	}
}

// newBuild checks a template build block.
func newBuild(config *Config, _ build.Component, _ build.Common) (build.Build, error) {
	switch {
	case config.Template == "":
		return nil, build.FieldErrorf("", "template is required for backend %q", Name)
	case !strings.HasSuffix(config.Template, ".in"):
		return nil, build.FieldErrorf("template", "%q does not end with .in", config.Template)
	}

	for name := range config.Vars {
		if strings.HasPrefix(name, "TT_") {
			return nil, build.FieldErrorf("vars."+name, "the TT_* variables are tt's")
		}
	}

	return templateBuild{config: *config}, nil
}

// templateBuild is a checked template build block.
type templateBuild struct {
	config Config
}

// The core finds these interfaces of a build by type assertions.
var (
	_ build.Preflighter = templateBuild{}
	_ build.InputHasher = templateBuild{}
)

// Preflight checks that envsubst is in the PATH of the build.
func (b templateBuild) Preflight(_ context.Context, c *build.Context) error {
	_, err := c.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%w: the %s backend runs %s, which is not in PATH",
			build.ErrNoTool, Name, tool)
	}

	return nil
}

// Inputs are what an incremental build reads besides the build block and
// the files of the component: the version of envsubst, and the template,
// which is outside the component when cwd is.
func (b templateBuild) Inputs(ctx context.Context, c *build.Context) ([]string, error) {
	version, err := c.Output(c.Command(ctx, tool, "--version"))
	if err != nil {
		return nil, err
	}

	firstLine, _, _ := bytes.Cut(version, []byte("\n"))

	data, err := os.ReadFile(filepath.Join(c.Dirs.Cwd, b.config.Template))
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(data)

	return []string{
		string(firstLine),
		b.config.Template + " sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

// Run renders the template into the share directory of the component.
func (b templateBuild) Run(ctx context.Context, c *build.Context) error {
	source, err := os.Open(filepath.Join(c.Dirs.Cwd, b.config.Template))
	if err != nil {
		return err
	}

	defer source.Close()

	cmd := c.Command(ctx, tool)

	cmd.Stdin = source
	for name, value := range b.config.Vars {
		cmd.Env = append(cmd.Env, name+"="+value)
	}

	out, err := c.Output(cmd)
	if err != nil {
		return err
	}

	name := strings.TrimSuffix(filepath.Base(b.config.Template), ".in")

	return os.WriteFile(filepath.Join(c.Dirs.Share, name), out, 0o644)
}
```

Дальше на этой странице подробно разбирается каждая часть.

## Что backend сообщает о себе {#what-the-backend-says-of-itself}

`build.Backend` представляет собой значение. Его поля сообщают ядру, что это за
backend:

| Поле | |
| --- | --- |
| `Name` | значение `backend`, выбирающее его: строчная буква, затем строчные буквы, цифры и дефисы; в рамках одного tt имена backend'ов не повторяются |
| `Produces` | типы результатов, создаваемых его сборками, каждый ровно один раз: `build.Module`, `build.Data`, `build.Program` (см. [Результаты](#results)) |
| `Config` | возвращает новую пустую конфигурацию: указатель на структуру с полями блока |
| `New` | проверяет блок и возвращает сборку; `build.Typed(newBuild)` строит её из функции, принимающей конфигурацию в виде её собственного типа `*Config` |
| `Incremental` | разрешает указание `incremental = true` в блоке |
| `OutputFrom` | есть ли в блоке поле `output` и кто копирует перечисленное в нём |
| `Exclude` | gitignore-шаблоны файлов, которые инструмент backend'а оставляет в компоненте и которые никогда не раскладываются: `/target/` у `cargo` |
| `NoSourceLayout` | отключает включение по умолчанию исходных файлов Lua и предсобранных модулей в раскладку компонента: для backend'ов вроде `go`, чьи исходники не входят в файлы пакета |
| `Hook` | разрешает указывать этот backend в хуках жизненного цикла `[hooks.pre_build]`, `[hooks.post_build]` или `[hooks.clean]` |

Ядро вызывает `Config` и `New` при каждой проверке манифеста: это делает каждая
команда, читающая манифест, включая `tt package add`, причём для одного блока
вызовы могут повторяться неоднократно. Ни та, ни другая функция не должны
создавать побочных эффектов и обе должны быть легковесными. `New` смотрит только
на сам блок и не получает каталогов проекта; необходимые сборке инструмент или
файл проверяются позже, в `Preflight`.

## Поля блока {#the-fields-of-the-block}

Ядро декодирует поля блока в структуру, возвращаемую `Config`. Ключом поля
служит его тег `toml` либо имя поля в Go, если тег не задан; `toml:"-"`
исключает поле, а поля встраиваемой структуры становятся полями блока. Полем
может быть строка, логическое значение, число, срез, отображение со строковыми
ключами, структура из таких полей, указатель на любой из этих типов либо
`any`. Указатель позволяет отличить незаданное поле от его нулевого значения.

Поля, присутствующие в каждом блоке, - `backend`, `cwd`, `env`, `output`,
`incremental` и `static` - принадлежат ядру. В структуре `Config` их нет:
ядро передаёт их в `New` в виде `build.Common`.

Декодирование выполняется строго. Ошибка в блоке backend'а `template`
приводит к завершению с кодом 1 любой команды, читающей манифест:

```text
⨯ invalid manifest: components.config.build.vars.PORT: must be a string, not an integer
⨯ invalid manifest: components.config.build.templat: unknown field in manifest version "0.1"
⨯ invalid manifest: components.config.build.make_target: is a field of backend make, not of "template"
```

В первой строке указано `vars = { PORT = 3301 }`. Во второй - ключ, которого
нет ни у одного backend'а. В третьей - ключ другого backend'а: в тексте ошибки
перечисляются backend'ы, у которых такое поле есть.

## Проверка блока {#check-the-block}

`New` получает декодированную конфигурацию, компонент и общие поля. Функция
возвращает сборку либо ошибку. Поле `New` в `build.Backend` принимает
конфигурацию как `any`, поскольку backend'ы с разными конфигурациями хранятся
в одном списке; `build.Typed` передаёт её в `newBuild` уже в виде типа
`*Config`.

Ошибка в конкретном поле оформляется как `build.FieldError`, создаваемый
функцией `build.FieldErrorf`. Поле `Field` содержит путь к полю относительно
блока в том виде, как оно записано в манифесте: `template`, `vars.TT_PACKAGE`,
`platforms.linux.cflags`, `sources[1]`. Пустой `Field` означает ошибку всего
блока. Ядро добавляет путь к блоку в начало сообщения:

```text
⨯ invalid manifest: components.config.build: template is required for backend "template"
⨯ invalid manifest: components.config.build.template: "defaults.lua" does not end with .in
⨯ invalid manifest: components.config.build.vars.TT_PACKAGE: the TT_* variables are tt's
```

Любая другая ошибка из `New` относится к блоку в целом. Тегов для обязательных
полей или значений по умолчанию нет: их проверяет `New`.

`build.Component` описывает компонент: его `Name`, путь `Path` относительно
проекта, `Namespace` и `Package`. `Wants` содержит типы результатов, запрошенные
манифестом у компонента помимо блока сборки (nil, если ничего не запрошено, как
и должно быть в манифесте). Ядро сверяет сборку с этими типами; backend,
самостоятельно выбирающий цель сборки, если она не указана в блоке, может
ориентироваться на них (так `cargo` и `zig` собирают программу с именем
компонента, когда в `Wants` указан только `build.Program`). Единственная форма
манифеста, использовавшая этот механизм, объявлена устаревшей, поэтому новый
backend может предоставить эти проверки ядру. `build.Common` содержит поля
`Cwd`, `Env`, `Output` и `Static` блока.

## Запуск сборки {#run-the-build}

Метод `Run` выполняет сборку компонента в контексте `build.Context`. Ядро
создаёт такой контекст для каждого компонента и каждой сборки.

| Поле | |
| --- | --- |
| `Component` | компонент |
| `Target` | платформа в формате `GOOS` и `GOARCH`: всегда та машина, на которой работает tt; `ModuleExt` равен `.so` (или `.dylib` на darwin) |
| `Dirs` | каталоги сборки, см. ниже |
| `Tarantool` | Tarantool, под который собирается компонент: `Prefix`, `IncludeDir` (его заголовки C, `module.h` и `lua.h`), `Version`; поля пусты, если tt о них неизвестно |
| `Env` | переменные `env` блока и переменные `TT_*` сборки |
| `Fresh` | флаг, выставляемый `tt package build --rebuild`, а также для компонентов, чей блок изменился с момента прошлой сборки: очистить содержимое `Work` и собрать заново |
| `Locked` | флаг, задаваемый ключом `--locked`: инструменты сборки не должны менять версии; `LockedVersion` возвращает зафиксированную локфайлом версию rock'а |
| `Static` | значение `static` из блока |
| `Args` | аргументы, переданные командой модуля в эту сборку для её инструмента (`sdk.BuildOptions.Args`: `tt wasm build calc -- -opt=2`); при сборке через `tt package build` отсутствуют. Служат входными данными для инкрементальной сборки |
| `Hook` | флаг, выставляемый при выполнении хука жизненного цикла |
| `Log`, `Warn` | логгер сборки (логгер tt с атрибутом `backend=<name>`, чьи отладочные записи выводит `tt -V`) и функция для вывода предупреждений |

Каталоги (все пути абсолютные) для компонента пакета:

| Каталог | |
| --- | --- |
| `Project` | корень проекта: собираемый пакет, зависимость по пути или исходный rock |
| `Component` | каталог компонента |
| `Cwd` | место выполнения сборки: `cwd` из блока относительно проекта либо `Component` |
| `Work` | `_build/component/<name>/`, `TT_BUILD_DIR`: промежуточные файлы, сохраняемые между сборками; очищается при `--rebuild` и при изменении блока |
| `Modules` | каталог для модулей: `_build/rock/<product>/lib/<namespace>/`, `TT_OUTPUT_DIR` |
| `Share` | каталог для файлов данных: `_build/rock/<product>/lua/<namespace>/` |
| `Programs` | каталог для программ: `_build/rock/<product>/bin/`, общий для всех компонентов |
| `State` | `_build/`, состояние сборки проекта |
| `Tools` | `.tarantool/bin/` проекта |
| `Tree` | `.rocks/`, дерево rocks, куда устанавливаются зависимости |

Каталоги `Work`, `Modules`, `Share` и `Programs` ядро создаёт до вызова `Run`.

Контекст сообщает, для какого Tarantool выполняется сборка, но не то, как под
него компилировать: backend, компилирующий код на C, берёт заголовки из
`Tarantool.IncludeDir` (`TT_TARANTOOL_INCLUDE_DIR`) и компонует модуль так,
как его загружает Tarantool: с флагом `-shared` (или `-bundle -undefined dynamic_lookup`
на darwin) выбранным компилятором. `c` и `lua-c` (`modules/build/cc`) делают это
с помощью `CC` из окружения tt, подобно тому как LuaRocks собирает встроенный
rock.

### Команды {#commands}

`c.Command(ctx, name, args...)` возвращает `*exec.Cmd`, запускаемый в `Cwd`.
Его окружение состоит из переменных tt, затем `env` блока, затем `PATH` (в начало
которого помещается `.tarantool/bin` проекта, если он существует), и в конце -
переменных `TT_*`, чтобы блок не мог их переопределить. Backend может добавлять
переменные в `cmd.Env`: добавленное им имеет наивысший приоритет. Функция
`build.LookupEnv` читает переменную из `cmd.Env`. В приведённом примере так
передаются переменные `vars`.

- `c.Run(cmd)` выполняет команду так же, как tt запускает команды сборки:
  вывод отображается в реальном времени при запуске с `tt -V`, а без него -
  только в случае ошибки.
- `c.Output(cmd)` выполняет команду точно так же и возвращает её стандартный
  вывод, который не показывается: подходит для утилит, отвечающих в JSON, или
  для `envsubst`, чей вывод и составляет результат работы.
- `c.LookPath(name)` ищет программу в `PATH` сборки, и только там: если `PATH`
  задан в `env` блока, он становится единственным `PATH` сборки, и программа из
  собственного `PATH` процесса tt найдена не будет. Если программа не найдена,
  возвращается ошибка, оборачивающая `exec.ErrNotFound`; вызов `c.Command` для
  неё вернёт команду, завершающуюся этой ошибкой при попытке запуска.

К переменным `TT_*` относятся `TT_OUTPUT_DIR`, `TT_BUILD_DIR`,
`TT_COMPONENT_NAME`, `TT_PROJECT_ROOT`, `TT_PACKAGE`, `TT_VERSION`,
`TT_PLATFORM_OS`, `TT_PLATFORM_ARCH`, а также `TT_TARANTOOL_PREFIX` и
`TT_TARANTOOL_INCLUDE_DIR`, если они известны tt. Шаблон в примере может их
использовать: `${TT_PACKAGE}`, `${TT_VERSION}`. Утилита `envsubst` заменяет
любую переданную ей переменную, включая переменные окружения tt, а незаданные
переменные заменяет пустой строкой.

### Результаты {#results}

У всего, что создаёт сборка, есть тип. Тип определяет, куда попадает результат:

| Результат | Сборка помещает его в | Устанавливается в |
| --- | --- | --- |
| `build.Module`, скомпилированный модуль, загружаемый Tarantool | `Dirs.Modules` | `.rocks/lib/tarantool/<namespace>/` |
| `build.Data`, файл данных рядом с исходниками Lua, например `.wasm` или сгенерированный файл Lua | `Dirs.Share` | `.rocks/share/tarantool/<namespace>/` |
| `build.Program`, программа, запускаемая инстансами | `Dirs.Programs` | `.rocks/bin/`, где её находит `tt instance` |

Эти три типа соответствуют каталогам rock'а (`lib`, `share` и `bin`), которые
ядро раскладывает, учитывает и очищает и которые указываются в поле `to`
параметра `output`. Набор типов зафиксирован в ядре: backend сообщает, какие из
этих типов он создаёт, и не может добавлять свои; `Validate` отвергает backend,
указывающий неизвестный тип. Всё, что не является модулем или программой
(например, `.wasm`), считается файлом данных. Тип, добавленный в будущих
версиях SDK, получит собственный каталог в `Dirs` и элемент в `build.Results()`;
значения существующих типов (`module`, `data` и `program`) не меняются, поэтому
backend, корректно обрабатывающий создаваемые им типы, сохранит
работоспособность при появлении новых.

Одна сборка может создавать результаты всех трёх типов. Ядро считает
созданным то, что `Run` оставил в этих трёх каталогах (по типам), и удаляет
из них всё, что не было создано ни одной сборкой. Файл в каталоге того типа,
который backend не создаёт, приводит к ошибке сборки с кодом 2,
`build.ErrNotProduced`; то же относится к программе, заявленной через
`ProgramNamer` для backend'а без `build.Program`:

```text
⨯ building component "config": the build made what its backend does not produce: backend "template" makes no modules: lib/myapp/defaults.so
```

Файлы, оставшиеся от прошлой сборки другим backend'ом, ранее указанным в этом
блоке, не считаются созданными текущей сборкой и удаляются из пакета.

Затем ядро проверяет созданные файлы. Каждая программа должна быть исполняемым
файлом формата ELF или Mach-O для целевой платформы, а при указании
`static = true` в блоке - статическим исполняемым файлом; компонент с
устаревшим полем `kind` должен создавать артефакты, соответствующие этому
`kind`. Созданные каждым компонентом результаты записываются в
`_build/results.json`:

```json
{
  "components": {
    "config": {
      "data": [
        "defaults.lua"
      ]
    }
  }
}
```

Списки названы `modules`, `data` и `programs`, пути в них указаны относительно
соответствующего каталога. По этим записям сборка одного компонента знает,
что собрали остальные, а `tt instance` определяет, какой компонент создал
программу.

Backend, чей инструмент выводит файлы в другое место, копирует собранное в эти
каталоги с помощью `build.CopyFile`. Копирование выполняется без промежуточного
fork процесса tt, благодаря чему скопированную программу можно сразу запускать:
Linux запрещает запуск файла, открытого на запись, а процесс, форкнутый tt во
время записи, удерживал бы файл открытым вплоть до запуска собственной
программы.

Ошибка в `Run` приводит к остановке сборки с кодом 2:

```text
⨯ building component "config": open /home/me/myapp/config/missing.lua.in: no such file or directory
```

## Вывод: lib, share и bin {#output-lib-share-and-bin}

Поле `output` блока сборки перечисляет файлы, создаваемые сборкой, для
backend'а, чей инструмент записывает их по произвольным путям, а не в
каталоги контекста. Элемент списка может быть путём или таблицей с полями
`path` и `to`: `to = "lib"` обозначает модуль, `to = "share"` - файл данных,
`to = "bin"` - программу. Одиночный элемент можно записать без скобок:
`output = "dist/banner.txt"` вместо `output = ["dist/banner.txt"]`. Элемент без
`to` относится к единственному типу backend'а, у которого в `Produces` указан
только один тип (файл данных у backend'а файлов данных), и к модулю у любого
другого backend'а. Ядро передаёт эти элементы в `New` в поле `Common.Output`,
каждый в виде `build.OutputFile` с полями `Path` и типом `To`. Поле `OutputFrom`
определяет, поддерживается ли это поле backend'ом:

- `build.OutputNone`: поля `output` нет. Сборка сама раскладывает результаты
  по каталогам контекста, как делают `template`, `cargo` и `zig`. Блок с
  `output` будет отклонён:
  `components.config.build.output: is a field of backend make, shell or cmake, not of "template"`.
- `build.OutputCwd`: после успешного завершения `Run` ядро копирует каждый
  файл из списка `output` относительно каталога `Cwd` в каталог соответствующего
  типа под его базовым именем. Так работают `make` и `shell` (`modules/build/basic`).
- `build.OutputBackend`: backend сам читает `Common.Output` и копирует файлы
  из места их сборки. Так `cmake` копирует их из своего дерева сборки
  (`modules/build/cmake`).

Ядро отклоняет элемент, чьё значение `to` отсутствует в `Produces`, ещё до
начала сборки.

### Backend, чей вывод копирует ядро {#a-backend-whose-output-the-core-copies}

Backend `render` представляет собой вариант `template` для инструмента,
записывающего файл в каталог своего запуска: сборка рендерит шаблон в файл,
указанный в `output`, внутри `Dirs.Cwd`, а ядро копирует его в пакет.
`render.go`:

```go
package template

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/build"
)

// RenderConfig are the fields of a render build block.
type RenderConfig struct {
	// Template is the template, relative to the working directory of the
	// build. Required.
	Template string `toml:"template"`
}

// RenderBackend returns the render backend: it renders a template with
// envsubst into the one file output names, relative to the working
// directory of the build, which the core copies into the package.
func RenderBackend() build.Backend {
	return build.Backend{
		Name:       "render",
		Produces:   []build.Result{build.Data},
		Config:     func() any { return new(RenderConfig) },
		New:        build.Typed(newRender),
		OutputFrom: build.OutputCwd,
	}
}

// newRender checks a render build block: a template, and one file of output.
func newRender(config *RenderConfig, _ build.Component, common build.Common) (
	build.Build, error,
) {
	switch {
	case config.Template == "":
		return nil, build.FieldErrorf("", "template is required for backend %q", "render")
	case len(common.Output) != 1:
		return nil, build.FieldErrorf("output", "names %d files; render makes one",
			len(common.Output))
	}

	return renderBuild{template: config.Template, file: common.Output[0].Path}, nil
}

// renderBuild is a checked render build block.
type renderBuild struct {
	template string
	file     string
}

// Run renders the template into the file of output, in the working
// directory of the build.
func (b renderBuild) Run(ctx context.Context, c *build.Context) error {
	source, err := os.Open(filepath.Join(c.Dirs.Cwd, b.template))
	if err != nil {
		return err
	}

	defer source.Close()

	cmd := c.Command(ctx, tool)

	cmd.Stdin = source

	out, err := c.Output(cmd)
	if err != nil {
		return err
	}

	path := filepath.Join(c.Dirs.Cwd, b.file)

	err = os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		return err
	}

	return os.WriteFile(path, out, 0o644)
}
```

Конструктор `New` модуля (`module.go` ниже) предоставляет оба backend'а:
`Backends: []build.Backend{Backend(), RenderBackend()}`. После сборки модуля
в tt ([Встраивание в tt](#build-it-into-tt)) пакет может его использовать;
в `myapp/app.manifest.toml` из раздела [Использование в пакете](#use-it-in-a-package):

```toml
[components.banner]
path = "banner"

[components.banner.build]
backend = "render"
template = "banner.txt.in"
output = "dist/banner.txt"
```

`myapp/banner/banner.txt.in`:

```text
${TT_PACKAGE} ${TT_VERSION}
```

В блоке не указано `to`, а `render` создаёт только файлы данных, поэтому файл
считается файлом данных. Сборка выполняется в каталоге компонента `banner/`,
который служит рабочей директорией сборки при отсутствии `cwd` в блоке: сборка
записывает `banner/dist/banner.txt`, а ядро копирует его под базовым именем в
`share/` пространства имён компонента (имени пакета). Команда
`tt package build` устанавливает его в `.rocks/share/tarantool/myapp/`:

```console
$ ../dist/tt package build
$ cat banner/dist/banner.txt
myapp 0.0.0-dev.0
$ cat .rocks/share/tarantool/myapp/banner.txt
myapp 0.0.0-dev.0
$ ../dist/tt run -e "local fio = require('fio') \
    local file = fio.open('.rocks/share/tarantool/myapp/banner.txt') \
    io.write(file:read()) os.exit()"
myapp 0.0.0-dev.0
```

Код пакета на Lua может прочитать файл рядом с собой через
`fio.pathjoin(debug.sourcedir(), 'banner.txt')` в модуле пространства имён
`myapp`; другой код может читать его из каталога приложения (рабочей директории
его инстансов), как показано выше. Ни `banner.txt.in`, ни каталог `dist/` не
включаются в раскладку: стандартный шаблон `include` берёт только `*.lua` и
`*.so`. Ядро отклоняет запрошенное блоком, но не поддерживаемое `render`, ещё
до начала сборки с кодом 1, например при
`output = { path = "dist/banner.txt", to = "lib" }`:

```text
⨯ invalid manifest: components.banner.build.output[0].to: backend "render" makes no modules
```

Если `Run` завершается успешно, но не оставляет файла по пути из `output`,
сборка завершается с кодом 2: `building component "banner": copy declared output
"dist/banner.txt": opening the source: ...: no such file or directory`. Путь
в `output` для `build.OutputCwd` указывается относительно `Dirs.Cwd`: в блоке
с `cwd = "."` пишется `output = "banner/dist/banner.txt"` и шаблон задаётся
относительно проекта. Места установки для каждого `to` описаны в
[справочнике по манифесту (поле `output`)](../reference/app-manifest.md#output).

## Дополнительные возможности сборки {#more-of-the-build}

Сборка может реализовывать дополнительные интерфейсы. Ядро проверяет их
наличие с помощью type assertion, поэтому метод с иной сигнатурой никогда не
будет вызван: при проверке манифеста ядро отклоняет сборку с кодом 1, если её
метод `Preflight`, `Produces`, `Programs`, `Inputs` или `Clean` не совпадает с
сигнатурой интерфейса либо объявлен у указателя, в то время как `New` возвращает
значение:

```text
⨯ invalid manifest: components.config.build: invalid build backend "template": Preflight of its build template.templateBuild is func(*build.Context) error, not func(context.Context, *build.Context) error of build.Preflighter: the core would never call it
```

Функция `buildtest.Validate` также отклоняет такую сборку. Строки
`var _ build.Preflighter = templateBuild{}` в примере заставляют компилятор
сообщить о несоответствии заранее.

Интерфейс `build.Preflighter` проверяет требования сборки до того, как начнётся
сборка компонентов, благодаря чему отсутствие инструмента прерывает процесс
сразу, а не после длительной первой компиляции. Метод `Preflight(ctx, c)`
выполняется до того, как становится известна версия пакета: каталоги контекста
могут ещё не существовать, а переменная `TT_VERSION` пуста. Он выполняется также
до разрешения локфайла: `c.LockedVersion(rock)` возвращает данные из
`app.manifest.lock`, если локфайл актуален, и сообщает, что rock не зафиксирован,
если локфайл устарел или отсутствует (сборка разрешит его позже); при ключе
`--locked` устаревший локфайл приводит к ошибке, поэтому ответ в этом случае
точный. Контекст `ctx` соответствует процессу сборки: команды, запускаемые в
`Preflight`, создают с ним, чтобы они прерывались при отмене сборки. Ошибку
отсутствия инструмента в `PATH` оборачивают в `build.ErrNoTool`, чтобы отличить
отсутствующий инструмент от сбоя при сборке. Ошибка в `Preflight` приводит к
завершению сборки с кодом 2:

```text
⨯ building component "config": build tool not found: the template backend runs envsubst, which is not in PATH
```

Backend'ы tt, реализующие `Preflight`, вызывают его также и в начале `Run`.
Другие интерфейсы:

- `build.Producer`: метод `Produces()` сообщает, что именно запрашивает этот
  блок (каждый тип должен входить в `Backend.Produces`). Ядро проверяет это
  соответствие относительно `Component.Wants` до начала сборки. `cargo` и `zig`
  собирают модуль, программу или и то, и другое, ориентируясь на поля `module`
  и `bin`.
- `build.ProgramNamer`: метод `Programs()` возвращает имена программ,
  создаваемых сборкой, и сообщает, полон ли этот список. Ядро использует его
  для проверки того, что одну программу не создают два разных компонента, а
  команда `tt instance` определяет по нему компонент, создающий программу. Если
  список не исчерпывающий, ядро добавляет к нему всё, что сборка записала в
  `Programs`.
- `build.InputHasher`: см. следующий раздел.
- `build.Cleaner`: см. раздел [Очистка](#clean).

## Инкрементальные сборки {#incremental-builds}

Свойство `Incremental: true` разрешает указывать `incremental = true` в блоке.
В этом случае ядро пропускает сборку компонента, если его входные данные
совпадают с данными прошлой сборки, а оставленные той сборкой файлы всё ещё на
месте. Входными данными служат сам блок сборки и файлы компонента без учёта
файлов, игнорируемых правилами `.gitignore`. Хеш предыдущей сборки сохраняется в
`_build/cache/<component>.json`.

Сборка, читающая дополнительные данные, реализует `build.InputHasher`. Метод
`Inputs(ctx, c)` возвращает строки, которые ядро добавляет к входным данным.
`template` возвращает версию `envsubst` и хеш шаблона (который находится вне
компонента, если задан внешний `cwd`); `zig` возвращает свою версию и хеши
файлов `build.zig` и `build.zig.zon`. Метод `Inputs` вызывается при каждой
сборке, включая пропускаемые. Пропущенная сборка не вызывает `Run`, а флаг
`--rebuild` отключает пропуск:

```console
$ tt -V package build
   ...
   · running envsubst --version dir=/home/me/myapp/config
   · component is up to date component=config
```

Backend без `Incremental` отклоняет это поле:
`components.native.build.incremental: is a field of backend make, shell, zig or template, not of "cargo"`.

## Очистка {#clean}

Команда `tt package clean` удаляет результаты сборок пакета. Ядро удаляет то,
что создало само: при полной очистке пакета удаляется всё содержимое
`Dirs.State`, каталога `_build/` (`Dirs.Work` каждого компонента, протоколы
сборок, промежуточный каталог stage, в котором находятся `Dirs.Modules`,
`Dirs.Share` и `Dirs.Programs`), кроме файла блокировки проекта
`_build/tt.lock`; при вызове `tt package clean ИМЯ` удаляются `Dirs.Work`
компонента, его протоколы и его файлы в stage. В обоих случаях rock пакета
удаляется из `Dirs.Tree`. Backend'у, пишущему только в эти каталоги (как делает
`template`), больше ничего очищать не требуется.

Файлы, оставленные инструментом в любых других местах (каталог компонента,
`Dirs.Cwd`, `Dirs.Tools`), ядро не удаляет: оно не может отличить файл,
созданный сборкой, от файла проекта, а файл из списка `output` может быть
предсобранным файлом в git. Сборка, оставляющая файлы в таких местах,
реализует `build.Cleaner` и удаляет их в методе `Clean(ctx, c)`. Backend `render`
записывает файл из `output` в `Dirs.Cwd`; добавим в `render.go` (импортировав
`errors` и `io/fs`):

```go
// The core finds these interfaces of a build by type assertions.
var _ build.Cleaner = renderBuild{}

// Clean removes the file the build rendered into its working directory:
// the core removes the copy in the package, not what is beside the sources.
func (b renderBuild) Clean(_ context.Context, c *build.Context) error {
	err := os.Remove(filepath.Join(c.Dirs.Cwd, b.file))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	return err
}
```

Метод `Clean` вызывается для каждого очищаемого компонента этого backend'а до
того, как ядро начнёт что-либо удалять, благодаря чему каталог `Dirs.Work`
остаётся доступным для инструмента, самостоятельно очищающего своё дерево
сборки. Параметр `c` представляет собой контекст сборки компонента без `Args`.
Для компонента, который ни разу не собирался, каталоги контекста могут
отсутствовать, поэтому отсутствие файла ошибкой не считается. Контекст `ctx`
соответствует процессу очистки: команды в `Clean` создают с ним. Ошибка в
`Clean` приводит к завершению очистки с кодом 2, и ничего не удаляется. При
указании `--dry-run` вызовы `Clean` не производятся, а выводится список того,
что было бы очищено:

```console
$ ../dist/tt package build
$ ../dist/tt package clean --dry-run
would run the clean of component banner
would remove _build/cache
would remove _build/component
would remove _build/results.json
would remove _build/rock
would remove myapp 0.0.0.dev.0-1 from .rocks (4 files)
  .rocks/share/tarantool/rocks/myapp/0.0.0.dev.0-1/myapp-0.0.0.dev.0-1.rockspec
  .rocks/share/tarantool/myapp/banner.txt
  .rocks/share/tarantool/myapp/defaults.lua
  .rocks/share/tarantool/myapp/version.lua
$ ../dist/tt package clean
ran the clean of component banner
removed _build/cache
removed _build/component
removed _build/results.json
removed _build/rock
removed myapp 0.0.0.dev.0-1 from .rocks (4 files)
$ ls banner
banner.txt.in  dist
$ ls banner/dist
$ ls -A _build
tt.lock
```

Каталог `dist/` остаётся пустым: `Clean` удаляет только известный ему файл. В
тестах метод `harness.Clean(ctx, bld)` запускает `Clean` сборки точно так же,
как ядро, и не выполняет последующих действий:

```go
func TestRenderClean(t *testing.T) {
	banner := build.Component{Name: "banner", Path: "banner", Namespace: "app", Package: "app"}
	block := buildtest.Block{
		Common: build.Common{Output: []build.OutputFile{{Path: "dist/banner.txt", To: build.Data}}},
		Fields: map[string]any{"template": "banner.txt.in"},
	}

	bld, err := buildtest.Validate(template.RenderBackend(), banner, block)
	require.NoError(t, err)

	harness := buildtest.New(t, template.RenderBackend(), banner)
	require.NoError(t, os.WriteFile(filepath.Join(harness.Context.Dirs.Cwd, "banner.txt.in"),
		[]byte("${TT_PACKAGE}\n"), 0o600))
	require.NoError(t, harness.Run(t.Context(), bld))

	rendered := filepath.Join(harness.Context.Dirs.Cwd, "dist", "banner.txt")
	require.FileExists(t, rendered)

	require.NoError(t, harness.Clean(t.Context(), bld))
	assert.NoFileExists(t, rendered)
	require.NoError(t, harness.Clean(t.Context(), bld), "a file that is not there is no error")
}
```

То, что проект генерирует рядом с исходниками самостоятельно и о чём не знает ни
один backend, предназначено для хука `[hooks.clean]` манифеста, который
выполняется после backend'ов при очистке всего пакета; см.
[справочник](../reference/app-manifest.md#hookspre_build-hookspost_build-and-hooksclean).

## Блокировка проекта {#the-lock-of-the-project}

Ядро собирает компоненты пакета последовательно и удерживает блокировку
проекта, `_build/tt.lock`, на протяжении всей сборки, очистки и любой другой
команды, изменяющей проект. Другой процесс tt в это время не может изменять
`_build/`, `.rocks/`, манифест или локфайл: второй запуск `tt package build`
в том же проекте ожидает завершения первого. Для записи в каталоги своего
контекста backend'у не требуется собственная блокировка.

Процесс tt, запущенный сборкой в том же проекте (например, команда
`tt package fetch` в блоке `shell`), работает под блокировкой запустившего
его tt: удерживаемые блокировки перечисляются в `TT_PROJECT_LOCK`, которую
наследует каждая команда сборки (через `c.Command` или напрямую). Процесс tt,
запущенный сборкой в фоне без ожидания завершения, работал бы параллельно со
сборкой, поэтому сборка всегда дожидается завершения запущенных ею процессов.

## Хуки жизненного цикла {#lifecycle-hooks}

Хуки `[hooks.pre_build]` и `[hooks.post_build]` выполняются до и после сборок
всех компонентов, а `[hooks.clean]` - при вызове `tt package clean` для всего
пакета, после выполнения методов `Clean` backend'ов. В хуке указывается backend
со свойством `Hook: true` (в tt это `make` или `shell`); любой другой backend
отклоняется:
`hooks.pre_build: backend "template" is not supported (want make or shell)`.
Ядро проверяет блок хука так же, как блок сборки, передавая в `New` нулевое
значение `Component`. Сборка выполняется в контексте с выставленным флагом
`Hook`. У неё нет компонента, а в `Dirs` отсутствуют `Component`, `Work`,
`Modules`, `Share` и `Programs`. В `Env` отсутствуют `TT_OUTPUT_DIR`,
`TT_COMPONENT_NAME` и `TT_BUILD_DIR`, а файлы из списка `output` не
копируются.

## Проверка файлов пакета {#check-the-files-of-a-package}

Модуль может проверять файлы каждого собираемого tt пакета независимо от того,
каким backend'ом они были созданы. В структуре `build.FileCheck` задаётся
суффикс. Каждая сборка передаёт методу `Check` каждый файл пакета, имя которого
оканчивается на этот суффикс: файлы из раскладки компонентов и файлы данных,
созданные их сборками в `Share` (включая файлы пропущенных сборок). Модули и
программы на эту проверку не поступают: ядро проверяет их самостоятельно.

Backend `template` может рендерить JSON, а незаданная переменная оставляет JSON
некорректным. Файл `module.go` содержит конструктор модуля и проверку файлов
`.json`:

```go
package template

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/build"
)

// New is the constructor of the module: the template and render backends
// and the check of the .json files of a package, and no command.
func New(sdk.Services) sdk.Module {
	return sdk.Module{
		Backends:   []build.Backend{Backend(), RenderBackend()},
		FileChecks: []build.FileCheck{{Suffix: ".json", Check: checkJSON}},
	}
}

// checkJSON checks a .json file of a package, whatever made it: an empty
// one is a warning, one that is not JSON fails the build.
func checkJSON(_ context.Context, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	switch {
	case len(data) == 0:
		return path + " is empty", nil
	case !json.Valid(data):
		return "", fmt.Errorf("%s is not valid JSON", path)
	default:
		return "", nil
	}
}
```

Метод `Check` получает контекст сборки (для запускаемых им инструментов) и
абсолютный путь к файлу в stage пакета. Предупреждение выводится на экран, и
сборка продолжается. Ошибка прерывает сборку с кодом 1, при этом предупреждения
по остальным файлам не показываются. И предупреждения, и ошибки выводятся как
есть, поэтому они включают путь к файлу:

```text
⨯ /home/me/myapp/_build/rock/default/lua/myapp/settings.json is not valid JSON
```

Стандартный модуль `wasm` аналогично проверяет файлы `.wasm`
(`modules/wasm/filecheck.go`). В нём есть проверка и команды, но нет backend'а.

## Тестирование {#test-it}

Пакет `sdk/build/buildtest` проверяет блок и запускает сборку так же, как ядро.
`backend_test.go`:

```go
package template_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	template "example.com/tt-template"
	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// component is the component the tests build.
var component = build.Component{Name: "config", Path: "config", Namespace: "app", Package: "app"}

func TestBuild(t *testing.T) {
	bld, err := buildtest.Validate(template.Backend(), component, buildtest.Block{
		Fields: map[string]any{
			"template": "defaults.lua.in", "vars": map[string]any{"PORT": "3301"},
		},
	})
	require.NoError(t, err)

	harness := buildtest.New(t, template.Backend(), component)
	require.NoError(t, os.WriteFile(filepath.Join(harness.Context.Dirs.Cwd, "defaults.lua.in"),
		[]byte("return { port = ${PORT}, package = '${TT_PACKAGE}' }\n"), 0o600))
	require.NoError(t, harness.Run(t.Context(), bld))

	data, err := os.ReadFile(filepath.Join(harness.Context.Dirs.Share, "defaults.lua"))
	require.NoError(t, err)
	assert.Equal(t, "return { port = 3301, package = 'app' }\n", string(data))
}

func TestBlockErrors(t *testing.T) {
	for _, tc := range []struct {
		fields map[string]any
		want   string
	}{
		{map[string]any{}, `template is required for backend "template"`},
		{map[string]any{"template": "defaults.lua"},
			`template: "defaults.lua" does not end with .in`},
		{map[string]any{"template": "a.in", "var": "x"}, `var: unknown field`},
		{map[string]any{"template": "a.in", "vars": map[string]any{"PORT": int64(3301)}},
			`vars.PORT: must be a string, not an integer`},
		{map[string]any{"template": "a.in", "vars": map[string]any{"TT_PACKAGE": "x"}},
			`vars.TT_PACKAGE: the TT_* variables are tt's`},
	} {
		_, err := buildtest.Validate(template.Backend(), component,
			buildtest.Block{Fields: tc.fields})
		assert.EqualError(t, err, tc.want)
	}
}

func TestNoEnvsubst(t *testing.T) {
	bld, err := buildtest.Validate(template.Backend(), component, buildtest.Block{
		Fields: map[string]any{"template": "defaults.lua.in"},
	})
	require.NoError(t, err)

	harness := buildtest.New(t, template.Backend(), component)
	harness.Context.Env["PATH"] = t.TempDir()

	require.ErrorIs(t, harness.Run(t.Context(), bld), build.ErrNoTool)
}

func TestCheckJSON(t *testing.T) {
	checks := template.New(nil).FileChecks
	require.Len(t, checks, 1)

	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	bad := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(good, []byte(`{"port": 3301}`), 0o600))
	require.NoError(t, os.WriteFile(bad, []byte(`{"port": }`), 0o600))

	warning, err := checks[0].Check(t.Context(), good)
	require.NoError(t, err)
	assert.Empty(t, warning)

	_, err = checks[0].Check(t.Context(), bad)
	require.EqualError(t, err, bad+" is not valid JSON")
}
```

```console
$ go mod tidy
$ go test ./...
ok  	example.com/tt-template	0.010s
```

- `buildtest.Block` представляет блок сборки в виде, удобном для тестов:
  `Common`, `Incremental` и `Fields` - поля backend'а в том виде, как их
  создаёт TOML-декодер (строки, `int64`, `float64`, `bool`, `[]any` и
  `map[string]any`).
- `buildtest.Validate` проверяет backend так же, как ядро перед его
  добавлением, вызывая ту же функцию `build.Backend.Validate`: то, что
  отклоняет ядро (см. [Что отклоняет ядро](#what-the-core-refuses)), приводит к
  ошибке в тесте. Затем функция проверяет блок в порядке, принятом в ядре, и
  возвращает сборку либо первую ошибку (`*build.FieldError` для поля). В tt теста
  других backend'ов нет, поэтому ключ, не предусмотренный конфигурацией, будет
  здесь ошибкой `unknown field`, тогда как ядро могло бы сообщить, что это поле
  другого backend'а.
- `buildtest.New(t, backend, component)` создаёт `Context` для сборок
  `backend` в каталогах внутри `t.TempDir()`: `<tmp>/project` с компонентом
  внутри, а рядом - `work`, `lib`, `share`, `bin` и `state`. Целевая платформа -
  машина, на которой запущен тест, а сведения о Tarantool отсутствуют. В `Env`
  заданы переменные `TT_*` ядра: `TT_OUTPUT_DIR`, `TT_PROJECT_ROOT`,
  `TT_PACKAGE`, `TT_COMPONENT_NAME`, `TT_VERSION` (`buildtest.Version`,
  `0.0.0-dev.0`), `TT_PLATFORM_OS`, `TT_PLATFORM_ARCH` и `TT_BUILD_DIR`.
  Тест может изменить Context перед запуском сборки: `TestNoEnvsubst` очищает
  `PATH`, `Env["TT_VERSION"]` задаёт другую версию, а метод
  `harness.SetTarantool` указывает сборке данные Tarantool так же, как ядро -
  в `Tarantool` и в `TT_TARANTOOL_PREFIX` и `TT_TARANTOOL_INCLUDE_DIR`.
- `harness.Run` выполняет `Preflight` (если он есть у сборки) с пустой
  `TT_VERSION`, как это делает ядро, затем вызывает `Run` и возвращает ошибку,
  как и ядро, если `Run` оставил файл в каталоге того типа, который backend
  не создаёт. Метод не копирует файлы из `output`, что ядро делает для
  `build.OutputCwd`. Метод `Printed` возвращает вывод команд, `Warnings` -
  предупреждения сборки, а `Locked` задаёт версии, возвращаемые `LockedVersion`.
- `buildtest.ValidateHook` и `buildtest.NewHook(t, backend)` выполняют те же
  действия для backend'а, запускающего хуки.

## Встраивание в tt {#build-it-into-tt}

Рядом с модулем и рабочим деревом tt создадим `dist/tt.manifest.toml`:

```toml
manifest_version = "0.1"
manifest_kind = "tt-distribution"

[core]
path = "../tt"   # a checkout of tt whose SDK has sdk/build

[dependencies]
template = "../tt-template"
```

```console
$ cd dist
$ tt binary build tt
   • Resolving the core github.com/tarantool/tt/v3
   •   core github.com/tarantool/tt/v3 (path ../tt)
   •   standard aeon, build, emmylua, wasm (with the core)
   •   template example.com/tt-template (path ../tt-template)
   • Building tt
   • Built /home/me/dist/tt
/home/me/dist/tt
$ ./tt version --modules
Tarantool CLI version 3.0.0-alpha.2, linux/amd64. commit: 2f4ff650 (v3.0.0-alpha.2-187-g2f4ff650)
  aeon      (devel)  github.com/tarantool/tt/modules/aeon
  build     (devel)  github.com/tarantool/tt/modules/build
  builtin   (devel)  github.com/tarantool/tt/v3
  emmylua   (devel)  github.com/tarantool/tt/modules/emmylua
  template  (devel)  example.com/tt-template
  wasm      (devel)  github.com/tarantool/tt/modules/wasm
```

В `[core]` можно также указать коммит: `tt = "<commit>"`. Дистрибутив
сохраняет стандартные модули (включая `build`), поэтому в нём есть как
backend'ы tt, так и `template`. Все поля манифеста описаны в
[tt.manifest.toml](../reference/tt-manifest.md).

## Использование в пакете {#use-it-in-a-package}

`myapp/app.manifest.toml`:

```toml
manifest_version = "0.1"

[package]
name = "myapp"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

[components.config]
path = "config"

[components.config.build]
backend = "template"
template = "defaults.lua.in"
vars = { PORT = "3301" }
incremental = true
```

`myapp/config/defaults.lua.in`:

```lua
return { port = ${PORT}, package = '${TT_PACKAGE}', version = '${TT_VERSION}' }
```

```console
$ cd myapp
$ ../dist/tt package build
$ cat .rocks/share/tarantool/myapp/defaults.lua
return { port = 3301, package = 'myapp', version = '0.0.0-dev.0' }
$ ../dist/tt run -e "print(require('myapp.defaults').port)"
3301
```

Сам шаблон не входит в раскладку: стандартный `include` компонента включает
только `*.lua` и `*.so`.

Версия tt без этого модуля выдаёт предупреждение для команд, ничего не
собирающих (таких как `tt package resolve`), и останавливает сборку с кодом 1:

```text
⚠ components.config.build: no module of this tt provides backend "template"; its fields are not checked
⨯ building component "config": no module of this tt provides backend "template"
```

## Что отклоняет ядро {#what-the-core-refuses}

Ядро регистрирует backend'ы и проверки файлов каждого модуля при запуске tt.
Некорректно оформленные элементы отклоняются с указанием имени модуля:

- `Name`, не состоящее из строчной буквы и последующих строчных букв, цифр
  и дефисов, а также имя, уже предоставленное другим модулем: модули
  добавляются по алфавиту имён, и более поздний отклоняется;
- `Produces`, если оно пусто, указывает тип, отличный от `module`, `data` или
  `program`, либо повторяет какой-либо тип дважды;
- значение `OutputFrom`, не входящее в допустимую тройку, и пустой шаблон в
  `Exclude`;
- отсутствие `New`; отсутствие `Config` либо `Config`, возвращающий не указатель
  на структуру, или структура с полем, которое декодер не может заполнить,
  с двумя полями под один ключ либо с полем для общего ключа вроде `env`;
- `FileCheck` без `Suffix` или без `Check`.

Методы `build.Backend.Validate` и `build.FileCheck.Validate` выполняют эти же
проверки, за исключением проверки на совпадение имени с другим модулем; их
вызывают как ядро, так и `buildtest`, поэтому backend с успешно пройденными
тестами ядро примет.

Каждая ошибка отклонения выводится сразу, после чего бинарный файл завершается
с ошибкой при любом вызове из командной строки. Команда `tt binary build tt`
запускает `tt version` собранного бинарного файла, поэтому такой дистрибутив не
соберётся:

```text
⨯ the built binary does not start: .../tt version: exit status 1
  ⨯ module "bad": invalid build backend "Tmpl": the name must match [a-z][a-z0-9-]*
       module "bad": invalid build backend "noprod": the Produces must name module, data or program, once each
       module "bad": invalid build backend "envfield": env is a field of every build block, not one of a backend
       module "bad": invalid build backend "nonew": no New
       module "build": invalid build backend "make": module "bad" provides one of the name too
```

Здесь модуль `bad` предоставляет backend с именем `make`. По алфавиту имён он
идёт раньше `build`, поэтому отклоняется именно `make` из модуля `build`.

В манифесте ещё до начала сборки завершением с кодом 1 отклоняются: поле
другого backend'а, поле, которого нет ни у одного backend'а, значение
неверного типа, ошибка из `New`, сборка с методом опционального интерфейса,
который ядро никогда не вызовет, `output` или `incremental` у backend'а, не
поддерживающего их, элемент `output` или тип из `Producer`, который backend
не создаёт, а также хук с backend'ом без `Hook`. Во время сборки завершением
с кодом 2 отклоняются: ошибка в `Preflight` или `Run`, файл того типа, который
backend не создаёт, программа, не являющаяся исполняемой для целевой
платформы, а также модуль или программа, не являющиеся статическими при
указании `static = true`.

## Backend'ы tt {#the-backends-of-tt}

Backend'ы стандартного модуля `build` демонстрируют остальные части контракта:

| Пакет | Backend'ы | Что показывает |
| --- | --- | --- |
| `modules/build/basic` | `make`, `shell` | `OutputCwd`, `Hook`, `Producer` и `ProgramNamer` на основе `output` |
| `modules/build/cc` | `c`, `lua-c` | управление компилятором C с учётом `Tarantool` из контекста, поля `platforms.<os>` |
| `modules/build/cargo` | `cargo` | `Exclude`, `Component.Wants`, модуль, программа или и то, и другое |
| `modules/build/cmake` | `cmake` | `OutputBackend`, дерево сборки в `Work`, `Fresh` |
| `modules/build/gobuild` | `go` | `NoSourceLayout`, `static` |
| `modules/build/zig` | `zig` | `Incremental` с `InputHasher` |

Их поля описаны в
[справочнике по манифесту приложения](../reference/app-manifest.md#componentsnamebuild).




