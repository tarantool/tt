package binary

// BinaryContext contains the options shared by binary operations.
type BinaryContext struct {
	// Program is the program to manage. ProgramUnknown means all programs.
	Program Program
	// Verbose enables diagnostic output and output from build tools.
	Verbose bool
}
