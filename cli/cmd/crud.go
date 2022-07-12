package cmd

import (
	"fmt"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/crud"
	"github.com/tarantool/tt/cli/modules"
)

// NewCrudCmd creates a new crud command.
func NewCrudCmd() *cobra.Command {
	crudCmd := &cobra.Command{
		Use:   "crud",
		Short: "Interacting with the CRUD module",
	}
	crudCmd.AddCommand(NewImportCmd())

	return crudCmd
}

// crudImportFlags contains flags for crud import subcommand.
// Initialized with default values at creation.
var crudImportFlags = crud.ImportOpts{
	ConnectUsername: "",
	ConnectPassword: "",
	Format:          "csv",
	Header:          false,
	LogFileName:     "import",
	ErrorFileName:   "error",
	SuccessFileName: "success",
	Match:           "",
	BatchSize:       100,
	Progress:        false,
	Operation:       "insert",
	OnError:         "stop",
	NullVal:         "",
	Delimiter:       ",",
	Quote:           `"`,
}

// NewImportCmd creates a new import subcommand for crud command.
func NewImportCmd() *cobra.Command {
	importCmd := &cobra.Command{
		Use:   "import URI FILE SPACE",
		Short: "Import data from file into tarantool",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, "crud", &modulesInfo, internalImportModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	importCmd.Flags().StringVarP(&crudImportFlags.ConnectUsername, "username", "u",
		crudImportFlags.ConnectUsername, "connection username")
	importCmd.Flags().StringVarP(&crudImportFlags.ConnectPassword, "password", "p",
		crudImportFlags.ConnectPassword, "connection password")
	importCmd.Flags().StringVarP(&crudImportFlags.Format, "format", "",
		crudImportFlags.Format, "format of input data. Currently only <csv> is supported")
	importCmd.Flags().BoolVarP(&crudImportFlags.Header, "header", "",
		crudImportFlags.Header, "specifies that the first line is the header, "+
			"which describes the column names. Data begins from second line")
	importCmd.Flags().StringVarP(&crudImportFlags.LogFileName, "log", "",
		crudImportFlags.LogFileName, "name of log file with information about occurred errors. "+
			"If the file exists, the log continues to write to the existing file")
	importCmd.Flags().StringVarP(&crudImportFlags.ErrorFileName, "error", "",
		crudImportFlags.ErrorFileName, "name of file with rows that were not imported. "+
			"Overwrite existed file")
	importCmd.Flags().StringVarP(&crudImportFlags.SuccessFileName, "success", "",
		crudImportFlags.SuccessFileName, "name of file with rows that were imported. "+
			"Overwrite existed file")
	importCmd.Flags().StringVarP(&crudImportFlags.Match, "match", "", crudImportFlags.Match,
		"use correspondence between header fields in input file and target space fields. "+
			"Now it require option header as <true>. "+
			"If there are fields in the space format that are not specified in the header, "+
			"an attempt will be made to insert null into them. "+
			"If there are fields in the header that are not specified in the space format, "+
			"they will be ignored. Now only <header> value for this option is supported. "+
			"No yet possible to set a manual match, like <spaceId=csvFoo,spaceName=csvBar,...>")
	importCmd.Flags().Uint32VarP(&crudImportFlags.BatchSize, "batch-size", "",
		crudImportFlags.BatchSize, "crud batch size during import")
	importCmd.Flags().BoolVarP(&crudImportFlags.Progress, "progress", "",
		crudImportFlags.Progress, "progress file from last launch will be taken into account. "+
			"File stores the positions of lines that could not be imported at the last launch. "+
			"Also stores the stop position from the last start. "+
			"As a result, an attempt will be repeated to insert lines with specified positions, "+
			"and then work will continue from stop position. "+
			"At each launch, the content of the progress.json file is completely overwritten")
	importCmd.Flags().StringVarP(&crudImportFlags.Operation, "operation", "",
		crudImportFlags.Operation, "specifies which operation will be used to import tuples: "+
			"<insert> or <replace>")
	importCmd.Flags().StringVarP(&crudImportFlags.OnError, "on-error", "", crudImportFlags.OnError,
		"if any error occurs, either skips the problematic line and goes on or stops work. "+
			"Allows values <stop> or <skip>")
	importCmd.Flags().StringVarP(&crudImportFlags.NullVal, "null", "",
		crudImportFlags.NullVal, "sets value to be interpreted as NULL when importing. "+
			"By default, an empty value. Example for csv: field1val,,field3val, "+
			"where field2val will be taken as NULL")
	importCmd.Flags().StringVarP(&crudImportFlags.Delimiter, "delimiter", "",
		crudImportFlags.Delimiter, "sets the symbol that will be defined as field delimiter. "+
			"For csv by default is comma like RFC 4180. Cannot be <\\r>, <\\n>, "+
			"or the unicode replacement character (0xFFFD). To use a tab character as delimiter, "+
			"set this value as <tab>")
	importCmd.Flags().StringVarP(&crudImportFlags.Quote, "quote", "",
		crudImportFlags.Quote, "sets the symbol that will be defined as quote. "+
			"For csv by default is double quotes like RFC 4180. "+
			"Double symbol of this option acts as the escaping symbol within input data")

	return importCmd
}

// internalImportModule is a default import module.
func internalImportModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	switch len(args) {
	case 0:
		return fmt.Errorf("It is required to specify router URI.")
	case 1:
		return fmt.Errorf("It is required to specify input file.")
	case 2:
		return fmt.Errorf("It is required to specify target space.")
	}

	if crudImportFlags.BatchSize == 0 {
		return fmt.Errorf("The batch size must be greater than zero.")
	}

	if crudImportFlags.OnError != "skip" && crudImportFlags.OnError != "stop" {
		return fmt.Errorf("The option on-error can be <skip> or <stop>.")
	}

	if (crudImportFlags.Match == "header" && !crudImportFlags.Header) ||
		(crudImportFlags.Match != "" && crudImportFlags.Match != "header") {
		return fmt.Errorf("Currently only <header> value supported for match option. " +
			"Also it require option header as <true>.")
	}

	log.Infof("Running crud import:\n")
	if err := crud.RunImport(cmdCtx, &crudImportFlags, args[0], args[1], args[2]); err != nil {
		return err
	}

	return nil
}
