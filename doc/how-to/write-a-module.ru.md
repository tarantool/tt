# Написание модуля tt {#write-a-tt-module}

Модуль tt - это модуль Go, скомпилированный в
[дистрибутив tt](build-distribution.md): его пакет экспортирует конструктор,
возвращающий добавляемые им команды. Он построен на
[SDK tt](https://github.com/tarantool/tt/blob/v3/sdk/README.md) и больше
ничего из tt не импортирует. На этой странице с нуля пишется модуль
`tt probe`, который выводит список экземпляров приложения, отправляет ping
экземпляру и делает на нём снапшот, а затем тестируется и встраивается в tt.
О том, как вместо пересборки добавить команду отдельным исполняемым файлом,
который запускает tt, см. [Внешние модули](external-modules.md).

SDK находится в статусе alpha: его API может измениться в любом релизе tt.

## Настройка модуля Go {#set-up-the-go-module}

```sh
mkdir tt-probe && cd tt-probe
go mod init example.com/tt-probe
go get github.com/tarantool/tt/sdk@9997ea8b \
    github.com/tarantool/tt/sdk/connect@9997ea8b \
    github.com/tarantool/tt/sdk/dial@9997ea8b
go get github.com/tarantool/go-tarantool/v3 github.com/stretchr/testify
```

У модулей SDK пока нет версий: у tt нет тегов `sdk/...`, а его собственные
теги вроде `v3.0.0-alpha.2` версионируют ядро, `github.com/tarantool/tt/v3`,
и никакой другой модуль. Модули SDK подключают по коммиту tt, из которого
Go формирует псевдоверсию:

```text
require (
	github.com/tarantool/tt/sdk v0.0.0-20261105160839-9997ea8b6840
	github.com/tarantool/tt/sdk/connect v0.0.0-20261105160839-9997ea8b6840
	github.com/tarantool/tt/sdk/dial v0.0.0-20261105160839-9997ea8b6840
)
```

`9997ea8b` - коммит релиза `v3.0.0-alpha.2` (`git rev-parse
'v3.0.0-alpha.2^{commit}'` в клоне tt); берут коммит ядра, под которое
строится модуль. Все модули SDK подключают по одному и тому же коммиту,
включая `sdk/connect`, даже если модуль его не импортирует: в `go.mod`
модуля `sdk` он прописан на `v0.0.0-0` - плейсхолдер, который разрешают
только директивы `replace` в репозитории самого tt, а `go get` для одного
лишь `sdk` завершается с ошибкой `unknown revision sdk/connect/v0.0.0-0`.
Плейсхолдер сортируется ниже любой псевдоверсии, поэтому коммит, переданный
в `go get`, встаёт на его место. В `sdk/cluster` есть два таких
плейсхолдера: для `sdk/connect` и `sdk/dial`. После `v3.0.0-alpha.2` модуль
`sdk` аналогично требует `sdk/build` на плейсхолдере: передают также и
`github.com/tarantool/tt/sdk/build@<commit>`.

Сборка дистрибутива эти версии не использует: она собирает каждый модуль на
SDK по коммиту его ядра, что бы ни требовал `go.mod` самого модуля. По этим
версиям запускается `go test` модуля, поэтому их держат на коммите ядра:
когда ядро обновляется, подтягивают модули SDK через `go get` на его новом
коммите и выполняют `go mod tidy`. О том, как собрать модуль на ещё не
отправленных в репозиторий изменениях SDK, см.
[Совместная разработка с ядром](#develop-together-with-the-core).

## Написание конструктора {#write-the-constructor}

`probe.go`:

```go
// Package probe is a tt module: tt probe lists the instances of an
// application, pings an instance and makes a snapshot on it.
package probe

import (
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
)

// New is the constructor of the module. It declares the commands and
// nothing else: the services other than Log are not ready yet.
func New(services sdk.Services) []sdk.Mount {
	group := &cobra.Command{
		Use:   "probe",
		Short: "Look at the instances of an application",
	}

	group.AddCommand(
		newInstancesCmd(services),
		newPingCmd(services),
		newSnapshotCmd(services),
	)

	return []sdk.Mount{{Path: "", Cmd: group}}
}
```

`New` представляет собой `sdk.Constructor`, `func(sdk.Services)
[]sdk.Mount`; сборка дистрибутива ищет его под этим именем в корневом
пакете модуля, если в его манифесте не указано иное. tt вызывает его один
раз на процесс, до того как сам сконфигурирован: из всех сервисов здесь
разрешено использовать только `Log`, а остальные вызывают panic с указанием
модуля и метода. Команды обращаются к сервисам в своих хуках, которые
выполняются позже. Конструктор объявляет команды и их флаги и ничего
больше: он не читает ни файлы, ни переменные окружения.

Модуль, который также предоставляет бэкенды сборки для поля `backend`
в блоке сборки компонента или проверки файлов пакета, объявляет вместо
этого `sdk.ModuleConstructor`, `func(sdk.Services) sdk.Module`:
возвращаемый им `sdk.Module` содержит `Commands`, `Backends` и
`FileChecks` для файлов пакета из `github.com/tarantool/tt/sdk/build`.
tt вызывает его так же, как и `sdk.Constructor`, а в дистрибутиве оба вида
модулей перечисляются одинаково.

`Mount` - это команда и путь к её родителю: `""` означает корень, а
`"replicaset"` помещает команду под `tt replicaset`. Группа по пути,
которую не предоставляет ни один модуль, создаётся автоматически, без
описания. Каждая команда создаётся заново, без родителя; ядро забирает её
под своё управление сразу после возврата из конструктора.

## Список экземпляров приложения {#list-the-instances-of-an-application}

`instances.go`:

```go
package probe

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/output"
)

// instancesResult is the result of tt probe instances. Its fields are what
// -o json and -o yaml print.
type instancesResult struct {
	App       string   `json:"app" yaml:"app"`
	Instances []string `json:"instances" yaml:"instances"`
}

// Human prints one instance per line.
func (r instancesResult) Human(w io.Writer) error {
	for _, name := range r.Instances {
		_, err := fmt.Fprintln(w, name)
		if err != nil {
			return err
		}
	}

	return nil
}

func newInstancesCmd(services sdk.Services) *cobra.Command {
	var lookup sdk.AppLookup

	cmd := &cobra.Command{
		Use:   "instances APP",
		Short: "List the instances of an application",
		Args:  cobra.ExactArgs(1),
	}

	format := output.BindFormat(cmd.Flags(), output.FormatHuman,
		output.FormatHuman, output.FormatJSON, output.FormatYAML)
	sdk.AddAppFlags(cmd, &lookup)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		app, instance := sdk.SplitInstance(args[0])
		if instance != "" {
			return sdk.Usagef("%q names an instance: give the application only", args[0])
		}

		cfg, err := services.ClusterConfig(cmd.Context(),
			sdk.AppSource(app).WithLookup(lookup))
		if err != nil {
			return err
		}

		names, err := sdk.Instances(cfg.Config)
		if err != nil {
			return err
		}

		services.Log().Debug("Read the cluster configuration", "app", app, "dir", cfg.Dir)

		printer, err := services.Streams().Printer(format.Format())
		if err != nil {
			return err
		}

		return printer.Emit(instancesResult{App: app, Instances: names})
	}

	return cmd
}
```

- `sdk.AddAppFlags` объявляет флаги `--scope` и `--template` со справкой и
  автодополнением, аналогичными командам tt для экземпляров, а
  `sdk.AppSource(app).WithLookup(lookup)` находит приложение тем же
  способом, что и они: проект, в котором запущен tt, если имя совпадает,
  иначе область установки системы. `sdk.SplitInstance` разделяет `APP:INSTANCE`.
- `Services.ClusterConfig` возвращает конфигурацию кластера источника в том
  виде, как её видит Tarantool 3, вместе с каталогом, относительно которого
  разрешаются относительные пути (`Dir`); если конфигурация отсутствует,
  возвращается ошибка, оборачивающая `sdk.ErrNotFound`. `sdk.Instances`
  перечисляет её экземпляры, а `sdk.InstanceConfig` разрешает конфигурацию
  одного из них. `sdk.ParseClusterSource` разбирает аргумент, который также
  может быть файлом конфигурации кластера или URI хранилища конфигурации,
  как это делает `tt cluster show`.
- `output.BindFormat` объявляет `-o/--format` с поддерживаемыми командой
  форматами и форматом по умолчанию. `Services.Streams().Printer` выводит
  результат в stdout: метод `Human` для человекочитаемого формата (`table`),
  а само значение - для `json` и `yaml` согласно их тегам `json` и `yaml`,
  поэтому у поля должны быть оба тега. stdout предназначен только для
  результата работы; журналы и интерактивные вопросы выводятся в stderr.
- `Services.Log` - логгер модуля: записи содержат `module=probe`, а
  отладочные сообщения отображаются при запуске с `tt -V`.
  `log.LibraryOf` создаёт логгер для сторонней библиотеки, понижая её
  записи уровня Info до уровня Debug.
- Ошибка, возвращённая из `RunE`, выводится tt один раз и задаёт код выхода.
  `sdk.Usagef` и `sdk.WithUsage` помечают ошибку в способе вызова команды:
  tt выводит сообщение, а затем подсказку по использованию команды. Ошибки
  в `Args` или флагах также выводятся вместе с подсказкой.

## Подключение к экземпляру {#connect-to-an-instance}

`target.go`:

```go
package probe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/go-tarantool/v3"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/dial"
	"github.com/tarantool/tt/sdk/log"
)

// timeout bounds the connection and each request.
const timeout = 3 * time.Second

// connFlags are the flags of a command that connects to an instance.
type connFlags struct {
	username string
	password string
	lookup   sdk.AppLookup
}

// bind declares the flags on cmd.
func (f *connFlags) bind(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVarP(&f.username, "username", "u", "", "the user to connect as")
	flags.StringVarP(&f.password, "password", "p", "", "the password of the user")
	sdk.AddAppFlags(cmd, &f.lookup)
}

// listen is an entry of iproto.listen in the configuration of an instance.
type listen struct {
	URI    string            `yaml:"uri"`
	Params map[string]string `yaml:"params"`
}

// connect connects to target: a URI, with or without credentials, or
// APP:INSTANCE. A failure to connect is a failure of the system: tt exits 2.
func (f *connFlags) connect(
	ctx context.Context, services sdk.Services, target string,
) (*tarantool.Connection, error) {
	opts, err := f.dialOpts(ctx, services, target)
	if err != nil {
		return nil, err
	}

	dialer, err := dial.New(opts)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := tarantool.Connect(ctx, dialer, tarantool.Opts{
		Timeout:    timeout,
		SkipSchema: true,
		Logger:     log.LibraryOf(services.Log(), "go-tarantool"),
	})
	if err != nil {
		return nil, sdk.WithCode(sdk.ExitSystem,
			fmt.Errorf("connecting to %s: %w", log.RedactTarget(target), err))
	}

	return conn, nil
}

// dialOpts returns the options of a dialer to target. The credentials are
// the ones a URI carries, which exclude -u and -p; otherwise -u and -p, and
// an empty one is taken from TT_CLI_USERNAME or TT_CLI_PASSWORD, as tt
// connect takes them.
func (f *connFlags) dialOpts(
	ctx context.Context, services sdk.Services, target string,
) (dial.Opts, error) {
	opts := dial.Opts{Auth: tarantool.AutoAuth, User: f.username, Password: f.password}
	params := map[string]string{}
	uri := target

	switch {
	case connect.IsCredentialsURI(target):
		if f.username != "" || f.password != "" {
			return opts, sdk.Usagef("the URI has credentials: -u and -p are not allowed")
		}

		uri, opts.User, opts.Password = connect.ParseCredentialsURI(target)
	case connect.IsBaseURI(target):
	default:
		entry, err := f.instanceListen(ctx, services, target)
		if err != nil {
			return opts, err
		}

		uri, params = entry.URI, entry.Params
	}

	if opts.User == "" {
		opts.User = os.Getenv(connect.TarantoolUsernameEnv)
	}

	if opts.Password == "" {
		opts.Password = os.Getenv(connect.TarantoolPasswordEnv)
	}

	log.RegisterSecret(opts.Password)

	// host:port?transport=ssl&ssl_ca_file=ca.crt
	base, query, err := connect.SplitURIQuery(uri)
	if err != nil {
		return opts, sdk.WithUsage(err)
	}

	for key := range query {
		params[key] = query.Get(key)
	}

	opts.Transport = params["transport"]
	opts.SslKeyFile = params["ssl_key_file"]
	opts.SslCertFile = params["ssl_cert_file"]
	opts.SslCaFile = params["ssl_ca_file"]
	opts.SslCiphers = params["ssl_ciphers"]

	network, address := connect.ParseBaseURI(base)
	opts.Address = network + "://" + address

	return opts, nil
}

// instanceListen returns the first iproto.listen entry of the instance
// target, APP:INSTANCE, names, with its SSL files relative to the
// application directory made absolute. The URI is taken as it is written.
func (f *connFlags) instanceListen(
	ctx context.Context, services sdk.Services, target string,
) (listen, error) {
	app, instance := sdk.SplitInstance(target)
	if instance == "" {
		return listen{}, sdk.Usagef("%q is neither a URI nor APP:INSTANCE",
			log.RedactTarget(target))
	}

	cfg, err := services.ClusterConfig(ctx, sdk.AppSource(app).WithLookup(f.lookup))
	if err != nil {
		return listen{}, err
	}

	instanceCfg, err := sdk.InstanceConfig(cfg.Config, instance)
	if err != nil {
		return listen{}, err
	}

	var entries []listen

	_, err = instanceCfg.Get(goconfig.NewKeyPath("iproto/listen"), &entries)
	if err != nil {
		return listen{}, fmt.Errorf("%s: iproto.listen: %w", target, err)
	}

	if len(entries) == 0 || entries[0].URI == "" {
		return listen{}, fmt.Errorf("%s: iproto.listen has no URI", target)
	}

	entry := entries[0]
	if entry.Params == nil {
		entry.Params = map[string]string{}
	}

	for key, value := range entry.Params {
		if strings.HasPrefix(key, "ssl_") && strings.HasSuffix(key, "_file") &&
			value != "" && !filepath.IsAbs(value) {
			entry.Params[key] = filepath.Join(cfg.Dir, value)
		}
	}

	return entry, nil
}
```

SDK предоставляет составные части, а модуль объединяет их воедино, включая
учётные данные:

1. `connect.IsCredentialsURI` сопоставляет URI с учётными данными,
   `user:password@host:port` и его unix-формами, а
   `connect.ParseCredentialsURI` извлекает их оттуда. `connect.IsBaseURI`
   сопоставляет URI без них: `host:port`, `tcp://host:port`, `unix/:path`,
   `unix://path` и путь, начинающийся с `/`, `./`, `../` или `~/`.
2. Всё остальное здесь считается `APP:INSTANCE`: в качестве URI берётся
   первая запись `iproto.listen` этого экземпляра, а `Dir` превращает пути к
   SSL-файлам из её параметров в абсолютные. В примере URI берётся как
   написано; подстановка `{{ instance_name }}` и превращение относительного
   пути к сокету в абсолютный не выполняются.
3. Приоритет учётных данных выбирает сам модуль, SDK никаких правил не
   навязывает. В примере логика повторяет `tt connect`: учётные данные в
   URI исключают `-u` и `-p`; иначе используются `-u` и `-p`, а незаданные
   берутся из `TT_CLI_USERNAME` или `TT_CLI_PASSWORD`
   (`connect.TarantoolUsernameEnv`, `connect.TarantoolPasswordEnv`).
   Строка `connect.EnvTarantoolCredentialsHelp` описывает обе эти
   переменные для вывода в справке команды.
4. `connect.SplitURIQuery` отделяет строку параметров (query) от URI, куда
   передаются `transport` и параметры `ssl_*`, а `connect.ParseBaseURI`
   возвращает тип сети и адрес, которые `dial.Opts.Address` принимает в виде
   `network://address`.
5. `dial.New` возвращает dialer для `tarantool.Connect` из go-tarantool. При
   пустом значении `Transport` выбирается SSL, если задано хотя бы одно
   SSL-поле, и обычное соединение в противном случае; значения `"plain"` и
   `"ssl"` задают выбор явно, любые другие вызывают ошибку. `Auth` задаёт
   метод аутентификации: `tarantool.AutoAuth` (`dial.AuthAuto`) выбирает
   метод сервера по SSL и chap-sha1 по обычному соединению, а
   `dial.ParseAuth` распознаёт значения `auto`, `chap-sha1` и `pap-sha256`,
   передаваемые параметром `auth_type` в URI. При pap-sha256 пароль
   передаётся в открытом виде: по обычному соединению `dial.New` отклоняет
   его с ошибкой `dial.ErrPapSha256Unencrypted`. SSL-dialer работает через
   реализацию TLS на чистом Go, а в сборках с тегом `openssl` - через
   OpenSSL.
6. `log.RegisterSecret` скрывает пароль во всех записях журнала. Результат
   работы и вопросы к пользователю к журналам не относятся: `log.RedactTarget`
   скрывает учётные данные цели, выводимой в них и в тексте ошибок.

Функция `connect.CreateURIOpts` разбирает URL со схемой и хостом, например
`https://user:pass@localhost:2379/prefix?timeout=5`, в структуру
`connect.URIOpts`: эндпоинт, префикс, учётные данные, SSL-файлы и таймаут
(по умолчанию 3 секунды). Она предназначена для хранилищ конфигураций etcd и
Tarantool, а не для URI iproto, у которых нет схемы;
`connect.MakeURLHelp` формирует справку для такого аргумента-URL.
Конфигурация кластера из хранилища читается методом
`Services.ClusterConfig` для `sdk.StorageSource`, который берёт учётные
данные сначала из URI, затем из `sdk.Credentials`, а после из
`TT_CLI_ETCD_USERNAME` и `TT_CLI_ETCD_PASSWORD` (либо `TT_CLI_USERNAME` и
`TT_CLI_PASSWORD` для хранилища конфигураций Tarantool).

## Коды завершения и подтверждения {#exit-codes-and-confirmations}

`ping.go`:

```go
package probe

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/tarantool/go-tarantool/v3"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/output"
)

// errCancelled is a snapshot the user did not confirm.
var errCancelled = errors.New("cancelled: confirm, or give --force")

// pingResult is the result of tt probe ping.
type pingResult struct {
	Target string `json:"target" yaml:"target"`
	Status string `json:"status" yaml:"status"`
}

// Human prints the status of the instance.
func (r pingResult) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "%s: %s\n", r.Target, r.Status)

	return err
}

func newPingCmd(services sdk.Services) *cobra.Command {
	var conn connFlags

	cmd := &cobra.Command{
		Use:   "ping (URI | APP:INSTANCE)",
		Short: "Connect to an instance and print its status",
		Long: "Connect to an instance and print box.info.status.\n\n" +
			"The URI is [user:password@]host:port, a path to a unix socket\n" +
			"or unix/:path, with an optional ?transport=ssl&ssl_ca_file=...\n\n" +
			connect.EnvTarantoolCredentialsHelp,
		Args: cobra.ExactArgs(1),
	}

	conn.bind(cmd)
	format := output.BindFormat(cmd.Flags(), output.FormatHuman,
		output.FormatHuman, output.FormatJSON, output.FormatYAML)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		instance, err := conn.connect(cmd.Context(), services, args[0])
		if err != nil {
			return err
		}
		defer instance.Close()

		var status []string

		err = instance.Do(tarantool.NewEvalRequest("return box.info.status")).
			GetTyped(&status)
		if err != nil {
			return fmt.Errorf("box.info.status: %w", err)
		}

		printer, err := services.Streams().Printer(format.Format())
		if err != nil {
			return err
		}

		return printer.Emit(pingResult{Target: log.RedactTarget(args[0]), Status: status[0]})
	}

	return cmd
}

func newSnapshotCmd(services sdk.Services) *cobra.Command {
	var (
		conn  connFlags
		force bool
	)

	cmd := &cobra.Command{
		Use:   "snapshot (URI | APP:INSTANCE)",
		Short: "Make a snapshot on an instance",
		Args:  cobra.ExactArgs(1),
	}

	conn.bind(cmd)
	cmd.Flags().BoolVarP(&force, "force", "f", false, "do not ask for confirmation")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !force {
			// Under tt --no-prompt this asks nothing and answers false.
			yes, err := services.Confirm(
				"Make a snapshot on "+log.RedactTarget(args[0])+"?", false)
			if err != nil {
				return err
			}

			if !yes {
				return errCancelled
			}
		}

		instance, err := conn.connect(cmd.Context(), services, args[0])
		if err != nil {
			return err
		}
		defer instance.Close()

		_, err = instance.Do(tarantool.NewEvalRequest("box.snapshot()")).Get()
		if err != nil {
			return fmt.Errorf("box.snapshot: %w", err)
		}

		services.Log().Info("Snapshot made", "target", log.RedactTarget(args[0]))

		return nil
	}

	return cmd
}
```

tt завершается с кодом ошибки, которую вернула команда (см.
[Exit codes](../reference/commands/errors.md)): 1 (`sdk.ExitFailure`) при
любой ошибке, 2 (`sdk.ExitSystem`) при системном сбое, 3 (`sdk.ExitPartial`)
для операций, завершившихся успешно лишь на части целей. Код задаётся
функциями `sdk.WithCode` и `sdk.Errorf`; код 1 никогда не перекрывает более
специфичный код глубже по цепочке. tt также завершается с кодом 2 при
ошибках сети, нехватке прав или места на диске даже без явно заданного кода;
в примере код задаётся явно, чтобы его видели тесты.

`Services.Confirm` задаёт вопрос, требующий ответа да или нет, в stderr и
читает ответ из stdin. При запуске с `tt --no-prompt` он ничего не спрашивает
и сразу возвращает значение по умолчанию, поэтому значением по умолчанию
выбирают безопасный вариант. Флаг вроде `--force` позволяет скриптам
выполнять действие без остановок.

`Services.Exit` сообщает об ошибке и завершает tt с соответствующим кодом:
это полезно для кода, который не может вернуть ошибку напрямую, например для
функций обратного вызова в сторонних библиотеках. Команда, способная вернуть
ошибку штатным образом, обязана её возвращать.

## Работа с проектом {#work-on-the-project}

`Services.Project` предоставляет проект, с которым работает команда, под
управлением тех же механизмов, что и команды группы `tt package`. `Dir` -
каталог запуска команды, а `Root` - проект, в котором она находится,
найденный по тем же правилам, что и командами tt для проектов: ближайший
каталог от `Dir` вверх с файлом `app.manifest.toml` или шаблоном экземпляра,
либо сам `Dir`, если ничего не найдено. Если проект расположен выше `Dir`,
tt один раз сообщает в stderr, с каким проектом выполняется работа.
Остальные методы оперируют каталогом `Root`.

`scaffold.go` объявляет компонент проверок в манифесте:

```go
package probe

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
)

func newScaffoldCmd(services sdk.Services) *cobra.Command {
	return &cobra.Command{
		Use:   "scaffold NAME",
		Short: "Declare a component of probes in the package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := services.Project()

			// The check, the edit and the resolution are one change of the
			// project: no other tt is to change it in between.
			ctx, unlock, err := project.Lock(cmd.Context())
			if err != nil {
				return err
			}

			defer unlock()

			manifest, err := project.Manifest()
			if err != nil {
				return err
			}

			if _, ok := manifest.Components[args[0]]; ok {
				return sdk.Usagef("component %q is declared already", args[0])
			}

			err = project.AddComponent(ctx, sdk.Component{
				Name:    args[0],
				Comment: "Probes, declared by tt probe scaffold.",
				Path:    args[0],
				Include: []string{"*.lua"},
			})
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(services.Streams().IO().Out,
				"declared component %s\n", args[0])

			_, err = project.Resolve(ctx, sdk.ResolveOptions{})

			return err
		},
	}
}
```

а `New` добавляет `newScaffoldCmd(services)` в список команд `tt probe`.
Команда `tt probe scaffold checks` в проекте дописывает в конец файла
`app.manifest.toml` через пустую строку:

```toml
# Probes, declared by tt probe scaffold. {#probes-declared-by-tt-probe-scaffold.}
[components.checks]
path = "checks"
include = ["*.lua"]
```

затем выводит `declared component checks` и заново разрешает
`app.manifest.lock`, как это делает `tt package resolve`, и всё это - под
блокировкой проекта.

- `Lock` захватывает блокировку проекта, которую удерживают изменяющие его
  команды `tt package`, `_build/tt.lock`, и возвращает контекст с этой
  блокировкой и функцию её снятия. Он ждёт, пока другой процесс tt удерживает
  блокировку, сообщая об этом пользователю, пока контекст не завершится;
  Ctrl-C прерывает ожидание. Методы, изменяющие проект - `AddComponent`,
  `UpdateComponent`, `RemoveComponent`, `Resolve`, `AddDependency`,
  `RemoveDependency`, `Install`, `Build` и `Clean` - сами захватывают
  блокировку каждый для себя; если им передан контекст, возвращённый
  `Lock`, они работают под уже захваченной блокировкой. Команда берёт
  блокировку на время составного изменения из нескольких шагов, таких как
  проверка и две правки в `scaffold` или `tt wasm bind`, который сначала
  записывает собственные файлы, а затем выполняет сборку. Метод, которому
  передан другой контекст, пока команда удерживает блокировку, будет ждать
  её освобождения бесконечно, и то же самое произойдёт в другой горутине, как
  если бы это был отдельный процесс tt: контекст необходимо передавать
  дальше. Процесс tt, запускаемый командой для этого проекта, работает под её
  блокировкой, путь к которой tt передаёт в `TT_PROJECT_LOCK`. Методы
  `Manifest`, `ReadLock` и `BuildContext` выполняют только чтение и
  блокировку не захватывают.
- `Manifest` возвращает содержимое манифеста в точности как оно написано:
  имя пакета, зависимости и зависимости для разработки, каждая со своей
  версией, реестром и путем, а также компоненты, каждый со своим путем,
  `include`, `exclude`, пространством имён и блоком сборки: `backend`, `cwd`,
  `env`, `output`, `incremental`, `static`, а в `Fields` - поля бэкенда в
  том виде, как их прочитал декодер TOML. Метод
  `ManifestBuild.Decode(backend)` декодирует их в структуру конфигурации
  бэкенда модуля с помощью декодера ядра; поле, для которого нет места в
  структуре, приводит к ошибке: команда читает блок, собираемый её
  бэкендом, точно так же, как его читает `tt package build`. Манифест
  только разбирается, но не валидируется; отсутствие манифеста в проекте
  вызывает ошибку, оборачивающую `sdk.ErrNotFound`.
- `AddComponent` добавляет компонент - его комментарий, `path`, `include`
  и блок сборки `sdk.ComponentBuild`, чьи `Backend` и `Fields` записываются в
  исходном порядке (значением поля может быть строка, bool, целое число, их
  срез или отображение, например `output = "dist/calc.wasm"` или
  `env = { PROFILE = "release" }`) - после остального содержимого манифеста,
  сохраняя все существующие строки и комментарии. Сформированный манифест
  перед записью проверяется точно так же, как его проверяет
  `tt package build`, с использованием бэкендов этого экземпляра tt: если
  имя уже объявлено, проверка отклоняет компонент, а в проекте без
  манифеста запись не выполняется. `UpdateComponent` изменяет поля
  компонента или его блока сборки по месту их записи, добавляет
  отсутствующие поля и удаляет указанные в `Remove`; `RemoveComponent`
  удаляет компонент, его таблицы и объявляющие его ключи. Ни один из этих
  трёх методов не обновляет локфайл и не требует наличия Tarantool: локфайл
  после них остаётся устаревшим до вызова `Resolve` или следующего
  `tt package build`. Поле, записанное внутри инлайн-таблицы, например
  `build = { backend = "make" }`, не редактируется: текст ошибки называет
  это поле.
- `Resolve` разрешает зависимости по манифесту и обновляет локфайл, как это
  делает `tt package resolve`, сохраняя все уже зафиксированные версии; при
  отсутствии Tarantool процесс разрешения работает точно так же, как
  `tt package build` без него. `ReadLock` возвращает зафиксированные в
  локфайле версии по каждому продукту и признак его актуальности, ничего не
  разрешая заново.
- `AddDependency` объявляет rock в секции `[dependencies]` или, при флаге
  `Dev`, в `[dev_dependencies]`, аналогично команде `tt package add`:
  запись получает указанное ограничение версии и реестр (если он задан), а
  результат изменений сообщает прежние значения. Команда, сохраняющая
  написанную кем-то декларацию, сначала читает `Manifest` и вместо этого
  вызывает `Resolve`, как делает `tt wasm prepare`. `RemoveDependency`
  удаляет зависимость, как `tt package remove`, а `Install` заполняет
  каталог `.rocks/` по локфайлу, как `tt package fetch`. Они и метод
  `Resolve` принимают серверы пакетов rock, которые задаются флагом
  `--registry` команды: модуль объявляет этот флаг самостоятельно со справкой
  из `tt package`, доступной через `sdk.RegistryFlagUsage`. Описание
  изменений перечисляет пакеты rock, чья зафиксированная версия изменилась;
  структура `sdk.DependencyMove` форматируется так же, как её выводит
  `tt package`: `name 1.0.0-1`, `name 1.0.0-1 -> 1.1.0-1` или
  `name 1.0.0-1 -> (dropped)`.
- `Build` собирает пакет или указанные в `Components` компоненты кодом
  `tt package build`: вызов
  `Build(ctx, sdk.BuildOptions{Components: []string{"calc"}})` эквивалентен
  выполнению команды `tt package build calc` вместе с её хуками, блокировкой,
  структурой каталогов файлов и проверками. Аргументы из `Args` пробрасываются в
  сборку этого единственного компонента через `Context.Args` для используемого
  им инструмента. Результат сообщает, что именно создал каждый компонент, по
  типам артефактов, куда это было установлено в `.rocks`, и была ли
  инкрементальная сборка актуальной. `BuildContext` возвращает `build.Context`,
  в котором выполняется сборка компонента, и результат, который его backend
  формирует по блоку сборки, ничего не собирая: команда модуля бэкенда запускает
  свои инструменты в том же окружении, какое предоставляется процессу сборки.
- `Clean` удаляет результаты сборки, аналогично `tt package clean`: вызов
  `Clean(ctx, sdk.CleanOptions{Components: []string{"calc"}})`
  эквивалентен `tt package clean calc`, запуская метод `Clean` бэкенда
  компонента `calc` ([build.Cleaner](write-a-build-backend.md#clean)) и
  удаляя его дерево сборки, записи о сборке, файлы в каталоге stage и rock
  пакета, чтобы следующий `Build` собрал его заново, даже если сборка была
  инкрементальной и файлы не менялись. Результат содержит удалённые пути и
  компоненты, чей backend выполнил очистку. Флаг `DryRun` отключает
  фактическое удаление. Команда `tt wasm clean ИМЯ` удаляет записанные
  модулем файлы, а затем под той же блокировкой `Lock` очищает связанное с
  компонентом состояние ядра.
- `ScopeDir` возвращает каталог `.tarantool` проекта - область установки
  (scope) проекта, куда устанавливает файлы команда `tt binary`; его подкаталог
  `bin/` идёт первым в переменной `PATH` процесса сборки
  (`build.Dirs.Tools`). Это не место хранения служебного состояния сборки,
  которое располагается в `_build/` (`build.Dirs.State`). Метод
  `PrepareEnvironment` создаёт скрипты `.env/activate.sh` и `deactivate.sh`,
  если они отсутствуют, аналогично первой установке через
  `tt binary install` в проект - это полезно модулям, устанавливающим туда
  свои инструменты.

Метод `Services.Rocks().Index` превращает переданный каталог в rock-сервер
находящихся в нём пакетов rock: он создаёт манифесты LuaRocks и
`tt-rocks-index.json` так же, как `tt package index`. Модуль, упаковывающий
пакеты rock для других, раздаёт их таким способом. Программы и исполняемые
инструменты, которые записывает модуль (например, распакованные из архива),
создаются через пакет `github.com/tarantool/tt/sdk/execfile`: он записывает
файл без промежуточного fork процесса tt, чтобы его можно было сразу же
запустить параллельно со стартом других процессов.

В тестах проект из `sdktest` не обращается к файловой системе: его манифест
задаётся через `sdktest.WithManifest`, метод `AddComponent` добавляет записи
в него, `AddedComponents` возвращает переданные компоненты, `Resolves` -
вызовы `Resolve`, а `Locks` - количество взятых блокировок `Lock` и число
ещё не освобождённых; его `Lock` ничего не ждёт, а `Clean` фиксирует свои
вызовы (`Cleans`) и отвечает в соответствии с настройкой
`sdktest.WithClean`:

```go
func TestScaffold(t *testing.T) {
	services := sdktest.New(t, sdktest.WithManifest(sdk.Manifest{
		Components: map[string]sdk.ManifestComponent{"lua": {Path: "."}},
	}))

	got := services.Run(probe.New, "probe", "scaffold", "checks")

	require.NoError(t, got.Err)
	assert.Equal(t, "declared component checks\n", got.Stdout)
	require.Len(t, services.AddedComponents(), 1)
	assert.Equal(t, "checks", services.AddedComponents()[0].Path)
	assert.Len(t, services.Resolves(), 1)

	taken, held := services.Locks()
	assert.Equal(t, 1, taken)
	assert.Zero(t, held, "released")

	again := sdktest.New(t, sdktest.WithManifest(sdk.Manifest{
		Components: map[string]sdk.ManifestComponent{"checks": {Path: "checks"}},
	}))

	got = again.Run(probe.New, "probe", "scaffold", "checks")

	var usage *sdk.UsageError
	require.ErrorAs(t, got.Err, &usage)
	assert.Empty(t, again.AddedComponents())
}
```

## Предоставление бэкенда сборки {#provide-a-build-backend}

Модуль также может собирать компоненты пакета tt: компонент, в блоке сборки
которого указано `backend = "template"`, собирается бэкендом с именем
`template`, предоставляемым модулем - реализацией `build.Backend` из пакета
`github.com/tarantool/tt/sdk/build` среди `Backends` его структуры
`sdk.Module`. Модуль может также проверять файлы любого пакета, собираемого
tt, независимо от создавшего их бэкенда, через `build.FileCheck` в списке
`FileChecks`. Собственные бэкенды tt тоже оформлены как модуль -
стандартный модуль `build` в каталоге `modules/build` репозитория tt; у
самого ядра собственных бэкендов нет.

В руководстве [Write a build backend](write-a-build-backend.md) такой модуль
пишется с нуля: поля блока сборки и их проверка, процесс сборки и его
каталоги, типы создаваемых артефактов, инкрементальная сборка, проверки
файлов, тесты с помощью `sdk/build/buildtest` и ограничения, накладываемые
ядром.

## Тестирование модуля {#test-it}

`probe_test.go`:

```go
package probe_test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"example.com/tt-probe"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// cluster is the cluster configuration of the application "app": nothing
// listens on the address of its storage.
const cluster = `
groups:
  g:
    replicasets:
      r:
        instances:
          router: {}
          storage:
            iproto:
              listen:
              - uri: '127.0.0.1:1'
`

func TestInstances(t *testing.T) {
	services := sdktest.New(t, sdktest.WithClusterConfig(sdk.AppSource("app"), cluster))

	got := services.Run(probe.New, "probe", "instances", "app", "-o", "json")

	require.NoError(t, got.Err)
	assert.JSONEq(t, `{"app": "app", "instances": ["router", "storage"]}`, got.Stdout)
}

func TestInstancesOfInstance(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "instances", "app:router")

	var usage *sdk.UsageError
	require.ErrorAs(t, got.Err, &usage)
	assert.Equal(t, sdk.ExitFailure, got.ExitCode())
}

func TestInstancesNoApp(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "instances", "app")

	require.ErrorIs(t, got.Err, sdk.ErrNotFound)
}

// closedAddress returns a local TCP address nothing listens on.
func closedAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return address
}

func TestPingUnreachable(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "ping", closedAddress(t))

	require.Error(t, got.Err)
	assert.Equal(t, sdk.ExitSystem, got.ExitCode())
}

func TestPingApp(t *testing.T) {
	services := sdktest.New(t, sdktest.WithClusterConfig(sdk.AppSource("app"), cluster))

	got := services.Run(probe.New, "probe", "ping", "app:storage")

	require.ErrorContains(t, got.Err, "connecting to app:storage")
	assert.Equal(t, sdk.ExitSystem, got.ExitCode())
}

func TestPingCredentialsTwice(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "ping", "-u", "admin",
		"admin:secret@"+closedAddress(t))

	var usage *sdk.UsageError
	require.ErrorAs(t, got.Err, &usage)
}

func TestSnapshotDeclined(t *testing.T) {
	services := sdktest.New(t, sdktest.WithAnswers(false))

	got := services.Run(probe.New, "probe", "snapshot", closedAddress(t))

	require.Error(t, got.Err)
	assert.Contains(t, got.Stderr, "Make a snapshot on")
	assert.Equal(t, sdk.ExitFailure, got.ExitCode())
}

func TestSnapshotNoPrompt(t *testing.T) {
	services := sdktest.New(t, sdktest.WithNoPrompt())

	got := services.Run(probe.New, "probe", "snapshot", closedAddress(t))

	require.ErrorContains(t, got.Err, "cancelled")
	assert.Empty(t, got.Stderr) // No question asked.
}
```

`sdktest.Run` монтирует команды конструктора в корень, как это делает ядро,
и выполняет команду; `sdktest.New` сначала принимает параметры:
`WithClusterConfig` задаёт источнику конфигурацию кластера (любая другая
считается ненайденной), `WithAnswers` задаёт ответы для `Confirm`,
`WithNoPrompt` настраивает `Confirm` на возвращение значения по умолчанию,
а `WithTarantool`, `WithProject`, `WithStdin` и `WithIntegrity` задают
остальные сервисы; `WithRoot`, `WithManifest` и `WithNoManifest` задают
проект ([Работа с проектом](#work-on-the-project)). Структура `Result`
содержит ошибку, stdout, stderr и записи журнала; `ExitCode` возвращает код,
который SDK присваивает ошибке без применения сетевых и связанных с правами
правил ядра. Подключение, которое отклонило бы ядро, приводит к провалу
теста. Для модуля с `sdk.ModuleConstructor` функции `sdktest.RunModule` и
`services.RunModule` выполняют команды возвращаемого им `sdk.Module`, а
также валят тест при обнаружении бэкенда или проверки файлов, которые ядро
отклонило бы при старте tt.

```console
$ go mod tidy
$ go test ./...
ok  	example.com/tt-probe	0.031s
```

## Встраивание в tt {#build-it-into-tt}

Рядом с модулем создаётся файл `dist/tt.manifest.toml`:

```toml
manifest_version = "0.1"
manifest_kind = "tt-distribution"

[core]
tt = "v3.0.0-alpha.2"

[distribution]
standard = false   # the core of v3.0.0-alpha.2 lists no standard modules

[dependencies]
probe = "../tt-probe"
```

```console
$ cd dist
$ tt binary build tt
$ ./tt version --modules
Tarantool CLI version 3.0.0-alpha.2, linux/amd64. commit: <unknown>
  builtin  v3.0.0-alpha.2  github.com/tarantool/tt/v3
  probe    (devel)         example.com/tt-probe
$ ./tt probe instances myapp -o yaml
app: myapp
instances:
  - a
  - b
$ ./tt probe ping 127.0.0.1:1
   ⨯ connecting to 127.0.0.1:1: failed to dial: dial tcp 127.0.0.1:1: connect: connection refused
$ echo $?
2
```

Имя в секции `[dependencies]` становится именем модуля в tt: в выводе
`tt version --modules`, в записях журнала и в ошибках, относящихся к его
командам. Создаваемые сборкой файлы `tt.manifest.lock` и `tt.manifest.sum`
сохраняют в git. После публикации модуля манифест указывает его путь и
версию: `probe = "example.com/tt-probe@v0.1.0"`. Все поля манифеста описаны
в [tt.manifest.toml](../reference/tt-manifest.md).

## Совместная разработка с ядром {#develop-together-with-the-core}

Для изменения в модуле может потребоваться изменение в SDK или ядре,
которого ещё нет ни в одном доступном коммите tt. Оба изменения можно
внести в локальный чекаут tt рядом с модулем, `../tt`, и сгенерировать с
помощью tt файл workspace Go, собирающий модуль на этом чекауте:

```console
$ cd dist
$ tt binary workspace --core ../tt
/home/me/src/tt-probe/go.work
```

`tt binary workspace` читает `tt.manifest.toml` так же, как
`tt binary build tt`, используя ядро из параметра `--core` или из
`[core].path`, если в манифесте указан локальный чекаут. Сформированный
`go.work` подключает каждый модуль из секции `[dependencies]`, заданный
через путь, и подменяет директивами `replace` ядро и все модули Go из его
репозитория (каждый `go.mod` внутри `sdk/` и стандартные модули) на их
каталоги в чекауте без указания версий. После этого Go игнорирует версии,
прописанные в `go.mod` модуля, включая ещё не отправленный коммит:

```text
// Written by tt binary workspace: the local modules of the distribution
// ../dist/tt.manifest.toml on the SDK of the core ../tt,
// whatever their go.mod requires. tt binary workspace --remove deletes it.
// Keep it out of git.

go 1.27.4

use (
	.
)

replace (
	github.com/tarantool/tt/modules/aeon => ../tt/modules/aeon
	github.com/tarantool/tt/modules/build => ../tt/modules/build
	github.com/tarantool/tt/modules/emmylua => ../tt/modules/emmylua
	github.com/tarantool/tt/modules/wasm => ../tt/modules/wasm
	github.com/tarantool/tt/sdk => ../tt/sdk
	github.com/tarantool/tt/sdk/backup => ../tt/sdk/backup
	github.com/tarantool/tt/sdk/build => ../tt/sdk/build
	github.com/tarantool/tt/sdk/cluster => ../tt/sdk/cluster
	github.com/tarantool/tt/sdk/connect => ../tt/sdk/connect
	github.com/tarantool/tt/sdk/dial => ../tt/sdk/dial
	github.com/tarantool/tt/sdk/fetch => ../tt/sdk/fetch
	github.com/tarantool/tt/sdk/integrity => ../tt/sdk/integrity
	github.com/tarantool/tt/sdk/tuple => ../tt/sdk/tuple
	github.com/tarantool/tt/v3 => ../tt
)
```

Команда `go` ищет файл `go.work` вверх от текущего каталога, поэтому tt
создаёт его там, где `go test` в каждом локальном модуле сможет его найти:
рядом с манифестом для модулей из его каталога (модуль, в репозитории
которого `tt.manifest.toml` лежит в корне с `path = "."`, либо дистрибутив с
модулями в подкаталогах) и в каталоге каждого остального модуля, как у
`tt-probe` здесь. Каждый такой файл использует все локальные модули
дистрибутива. Директива `go` берётся самой новой среди всех их файлов
`go.mod`; каталоги из секции `[replace]` манифеста также подменяются. Команда
`tt binary workspace` выводит пути созданных файлов, а с флагом `--dry-run`
показывает, что было бы записано.

Цикл разработки выглядит следующим образом:

1. Внести изменения в ядро в `../tt`: например, добавить функцию в `sdk`.
2. Использовать её в коде модуля и запустить `go test ./...` в `tt-probe`:
   модуль собирается на базе SDK из `../tt`.
3. Собрать дистрибутив на том же чекауте: выполнить
   `tt binary build tt --core ../tt` в каталоге `dist` (см.
   [Сборка на другом ядре](build-distribution.md#building-on-another-core)).
   Сборка не читает `go.work`: она выполняется с `GOWORK=off` на ядре,
   заданном в `--core`, а хэш содержимого локального модуля исключает
   `go.work` и `go.work.sum`, поэтому создание workspace не меняет локфайл.
4. После того как изменения ядра отправлены в репозиторий в коммите `<commit>`,
   удалить workspace и подключить SDK по этому коммиту, как описано в разделе
   [Настройка модуля Go](#set-up-the-go-module); собрать на нём
   дистрибутив с `[core] tt = "<commit>"`:

```console
$ cd ../dist
$ tt binary workspace --remove
/home/me/src/tt-probe/go.work
$ cd ../tt-probe
$ go get github.com/tarantool/tt/sdk@<commit> \
    github.com/tarantool/tt/sdk/build@<commit> \
    github.com/tarantool/tt/sdk/connect@<commit> \
    github.com/tarantool/tt/sdk/dial@<commit>
$ go mod tidy
$ go test ./...
ok  	example.com/tt-probe	0.005s
```

Параметр `--remove` удаляет файлы `go.work`, созданные tt, и связанные с ними
`go.work.sum`; чекаут для этого не требуется, а в манифесте снова можно
указать версию ядра. Файл `go.work`, созданный tt, перезаписывается им же;
чужой файл (без первой строки-комментария tt) перезаписывается только с
параметром `--force` и никогда не удаляется. В дистрибутиве для разработки
можно указать чекаут прямо в манифесте, `[core] path = "../tt"`, и тогда
обеим командам не потребуется передавать `--core`.

Если модуль начинает импортировать модуль SDK, который не указан в его
`go.mod` (например, добавленный изменением в ядре), его требуется добавить в
зависимости, однако `go get` пытается найти версию в сети даже при наличии
workspace, что до публикации коммита приведёт к ошибке. До отправки коммита
добавьте зависимость на плейсхолдер вручную:

```sh
go mod edit -require=github.com/tarantool/tt/sdk/build@v0.0.0-0
```

Команда `go get` на шаге 4 подставит на его место настоящий коммит.

`gopls` и построенные на нём редакторы подхватывают workspace, найденный
командой `go`: команда `go env GOWORK` в каталоге модуля выводит путь к нему;
после создания или удаления файла перезапустите языковой сервер. Файлы
`go.work` и `go.work.sum` не сохраняют в git, добавляя их в `.gitignore`
модуля: они содержат пути каталогов конкретной машины, а команда
`go mod tidy` всё равно их игнорирует.

Без tt такой же workspace создаётся командой `go` с отдельной директивой
`-replace` для каждого модуля из чекаута: каждого `go.mod` внутри `sdk/` и
модулей, которые `go.mod` самого tt подменяет каталогами. Список растёт
вместе с SDK; `tt binary workspace` читает его напрямую из чекаута.

```sh
cd tt-probe
go work init .
go work edit \
    -replace github.com/tarantool/tt/v3=../tt \
    -replace github.com/tarantool/tt/modules/aeon=../tt/modules/aeon \
    -replace github.com/tarantool/tt/modules/build=../tt/modules/build \
    -replace github.com/tarantool/tt/modules/emmylua=../tt/modules/emmylua \
    -replace github.com/tarantool/tt/modules/wasm=../tt/modules/wasm \
    -replace github.com/tarantool/tt/sdk=../tt/sdk \
    -replace github.com/tarantool/tt/sdk/backup=../tt/sdk/backup \
    -replace github.com/tarantool/tt/sdk/build=../tt/sdk/build \
    -replace github.com/tarantool/tt/sdk/cluster=../tt/sdk/cluster \
    -replace github.com/tarantool/tt/sdk/connect=../tt/sdk/connect \
    -replace github.com/tarantool/tt/sdk/dial=../tt/sdk/dial \
    -replace github.com/tarantool/tt/sdk/fetch=../tt/sdk/fetch \
    -replace github.com/tarantool/tt/sdk/integrity=../tt/sdk/integrity \
    -replace github.com/tarantool/tt/sdk/tuple=../tt/sdk/tuple
```

Команда `go work use ../tt/sdk ...` вместо подмен работает только до тех
пор, пока `go.mod` модуля требует SDK на коммите, который Go может скачать:
команда `go` по-прежнему читает `go.mod` каждой версии, от которой зависит
используемый модуль, а коммит, ещё не отправленный в репозиторий, вызывает
ошибку `invalid version: unknown revision`. Подмена без версии ничего не
скачивает и не читает.

## Что отклоняет ядро {#what-the-core-refuses}

tt монтирует команды всех модулей до запуска какой-либо из них и отклоняет
команды, которые дерево не может принять, называя модуль; после этого
бинарный файл завершается с ошибкой на любой командной строке.

- Флаги: команда модуля не может объявлять `-h`/`--help`, `-C`/`--directory`,
  флаги корневой команды (`-I`/`--internal`, `-V`/`--verbose`,
  `--log-format`, `--no-prompt`, а в дистрибутиве с проверкой целостности -
  также `--integrity-check` и `--integrity-check-period`), а также
  persistent-флаги группы, под которую она смонтирована, как по имени, так и
  по короткому варианту. Короткий флаг, использованный дважды внутри команд
  одного модуля, также отклоняется.
- Имена: команда, чьё имя или псевдоним уже заняты рядом с ней, отклоняется -
  как дубликат при совпадении имён, либо как неоднозначность при совпадении
  имени с псевдонимом; аналогично отклоняются команды `help`, `__complete` и
  `__completeNoDesc` в корне. Команда с именем, совпадающим со встроенной
  командой tt в корне, считается дубликатом: чтобы заменить встроенную
  команду tt, собирают дистрибутив с параметром `builtin = false` либо
  используют внешний модуль.
- Пути: путь монтирования указывает команды по их именам, а не псевдонимам,
  и не может вести под исполняемую команду другого модуля. Группа, не
  являющаяся отдельной командой, считается общей: любой модуль может
  монтировать команды под неё.

Как выполняются команды:

- Корневые флаги указываются до команды: `tt -V probe ping ...`, а не
  `tt probe ping -V`, где `-V` считается неизвестным флагом. Команды модуля
  не видят `-V` или `--no-prompt` напрямую; они видят результат их действия
  через сервисы `Log` и `Confirm`.
- Не-persistent флаг группы разбирается только между группой и подкомандой,
  например `tt probe --flag ping`, поскольку корень tt обходит группы
  дерева команд, тогда как корень sdktest этого не делает. Флаг, общий для
  всех подкоманд, должен объявляться среди persistent-флагов группы.
- Ядро оборачивает хуки `PersistentPreRunE`, `PreRunE`, `RunE`, `PostRunE` и
  `PersistentPostRunE`, чтобы выводить возвращаемые ими ошибки. Хуки без
  суффикса `E` ничего для вывода не возвращают.
- `Services.Project().Dir()` возвращает рабочий каталог, в котором запущен
  tt. Модуль не может объявлять флаг `-C`; только команда, смонтированная
  под `tt package`, наследует его `-C` и получает этот каталог, который для
  неё становится также и `Root`.
