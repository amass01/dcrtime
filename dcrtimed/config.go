// Copyright (c) 2013-2014 The btcsuite developers
// Copyright (c) 2015-2020 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/decred/dcrd/dcrutil/v4"
	v1 "github.com/decred/dcrtime/api/v1"
	v2 "github.com/decred/dcrtime/api/v2"
	flags "github.com/jessevdk/go-flags"
)

const (
	defaultConfigFilename = "dcrtimed.conf"
	defaultDataDirname    = "data"
	defaultLogLevel       = "info"
	defaultLogDirname     = "logs"
	defaultLogFilename    = "dcrtimed.log"

	defaultMainnetPort = "49152"
	defaultTestnetPort = "59152"

	walletClientCertFile = "client.pem"
	walletClientKeyFile  = "client-key.pem"
)

var (
	defaultHomeDir       = dcrutil.AppDataDir("dcrtimed", false)
	defaultConfigFile    = filepath.Join(defaultHomeDir, defaultConfigFilename)
	defaultDataDir       = filepath.Join(defaultHomeDir, defaultDataDirname)
	defaultHTTPSKeyFile  = filepath.Join(defaultHomeDir, "https.key")
	defaultHTTPSCertFile = filepath.Join(defaultHomeDir, "https.cert")
	defaultLogDir        = filepath.Join(defaultHomeDir, defaultLogDirname)
	defaultAPIVersions   = fmt.Sprintf("%v,%v", v1.APIVersion, v2.APIVersion)
	defaultConfirmations = 6
	defaultMaxDigests    = 20
)

// runServiceCommand is only set to a real function on Windows.  It is used
// to parse and execute service commands specified via the -s flag.
var runServiceCommand func(string) error

// config defines the configuration options for dcrd.
//
// See loadConfig for details on the configuration load process.
type config struct {
	HomeDir           string   `short:"A" long:"appdata" description:"Path to application home directory."`
	ShowVersion       bool     `short:"V" long:"version" description:"Display version information and exit."`
	ConfigFile        string   `short:"C" long:"configfile" description:"Path to configuration file."`
	DataDir           string   `short:"b" long:"datadir" description:"Directory to store data."`
	LogDir            string   `long:"logdir" description:"Directory to log output."`
	TestNet           bool     `long:"testnet" description:"Use the test network."`
	SimNet            bool     `long:"simnet" description:"Use the simulation test network."`
	Profile           string   `long:"profile" description:"Enable HTTP profiling on given port -- NOTE port must be between 1024 and 65536."`
	CPUProfile        string   `long:"cpuprofile" description:"Write CPU profile to the specified file."`
	MemProfile        string   `long:"memprofile" description:"Write mem profile to the specified file."`
	DebugLevel        string   `short:"d" long:"debuglevel" description:"Logging level for all subsystems {trace, debug, info, warn, error, critical} -- You may also specify <subsystem>=<level>,<subsystem2>=<level>,... to set the log level for individual subsystems -- Use show to list available subsystems."`
	Listeners         []string `long:"listen" description:"Add an interface/port to listen for connections (default all interfaces port: 49152, testnet: 59152)."`
	WalletHost        string   `long:"wallethost" description:"Hostname for wallet server."`
	WalletCert        string   `long:"walletcert" description:"Certificate path for wallet server."`
	WalletPassphrase  string   `long:"walletpassphrase" description:"Passphrase for wallet server."`
	WalletClientCert  string   `long:"cert" description:"Path to TLS certificate for wallet gprc client authentication."`
	WalletClientKey   string   `long:"key" description:"Path to TLS client authentication key for wallet gprc."`
	Version           string
	HTTPSCert         string   `long:"httpscert" description:"File containing the https certificate file."`
	HTTPSKey          string   `long:"httpskey" description:"File containing the https certificate key."`
	StoreHost         string   `long:"storehost" description:"Enable proxy mode - send requests to the specified ip:port."`
	StoreCert         string   `long:"storecert" description:"File containing the https certificate file for storehost."`
	EnableCollections bool     `long:"enablecollections" description:"Allow clients to query collection timestamps."`
	Confirmations     int32    `long:"confirmations" description:"Amount of confirmations necessary to return timestamp proof."`
	MaxDigests        int32    `long:"maxdigests" description:"Max number of digests that can be queried"`
	APITokens         []string `long:"apitoken" description:"Token used to grant access to privileged API resources."`
	APIVersions       string   `long:"apiversions" description:"Enables API versions on the daemon."`
}

// serviceOptions defines the configuration options for the daemon as a service
// on Windows.
type serviceOptions struct {
	ServiceCommand string `short:"s" long:"service" description:"Service command {install, remove, start, stop}"`
}

// cleanAndExpandPath expands environment variables and leading ~ in the
// passed path, cleans the result, and returns it.
func cleanAndExpandPath(path string) string {
	// Expand initial ~ to OS specific home directory.
	if strings.HasPrefix(path, "~") {
		homeDir := filepath.Dir(defaultHomeDir)
		path = strings.Replace(path, "~", homeDir, 1)
	}

	// NOTE: The os.ExpandEnv doesn't work with Windows-style %VARIABLE%,
	// but they variables can still be expanded via POSIX-style $VARIABLE.
	return filepath.Clean(os.ExpandEnv(path))
}

// validLogLevel returns whether or not logLevel is a valid debug log level.
func validLogLevel(logLevel string) bool {
	switch logLevel {
	case "trace", "debug", "info", "warn", "error", "critical":
		return true
	}
	return false
}

// supportedSubsystems returns a sorted slice of the supported subsystems for
// logging purposes.
func supportedSubsystems() []string {
	// Convert the subsystemLoggers map keys to a slice.
	subsystems := make([]string, 0, len(subsystemLoggers))
	for subsysID := range subsystemLoggers {
		subsystems = append(subsystems, subsysID)
	}

	// Sort the subsytems for stable display.
	sort.Strings(subsystems)
	return subsystems
}

// parseAndSetDebugLevels attempts to parse the specified debug level and set
// the levels accordingly.  An appropriate error is returned if anything is
// invalid.
func parseAndSetDebugLevels(debugLevel string) error {
	// When the specified string doesn't have any delimters, treat it as
	// the log level for all subsystems.
	if !strings.Contains(debugLevel, ",") && !strings.Contains(debugLevel, "=") {
		// Validate debug log level.
		if !validLogLevel(debugLevel) {
			str := "the specified debug level [%v] is invalid"
			return fmt.Errorf(str, debugLevel)
		}

		// Change the logging level for all subsystems.
		setLogLevels(debugLevel)

		return nil
	}

	// Split the specified string into subsystem/level pairs while detecting
	// issues and update the log levels accordingly.
	for _, logLevelPair := range strings.Split(debugLevel, ",") {
		if !strings.Contains(logLevelPair, "=") {
			str := "the specified debug level contains an invalid " +
				"subsystem/level pair [%v]"
			return fmt.Errorf(str, logLevelPair)
		}

		// Extract the specified subsystem and log level.
		fields := strings.Split(logLevelPair, "=")
		subsysID, logLevel := fields[0], fields[1]

		// Validate subsystem.
		if _, exists := subsystemLoggers[subsysID]; !exists {
			str := "the specified subsystem [%v] is invalid -- " +
				"supported subsytems %v"
			return fmt.Errorf(str, subsysID, supportedSubsystems())
		}

		// Validate log level.
		if !validLogLevel(logLevel) {
			str := "the specified debug level [%v] is invalid"
			return fmt.Errorf(str, logLevel)
		}

		setLogLevel(subsysID, logLevel)
	}

	return nil
}

// removeDuplicateAddresses returns a new slice with all duplicate entries in
// addrs removed.
func removeDuplicateAddresses(addrs []string) []string {
	result := make([]string, 0, len(addrs))
	seen := map[string]struct{}{}
	for _, val := range addrs {
		if _, ok := seen[val]; !ok {
			result = append(result, val)
			seen[val] = struct{}{}
		}
	}
	return result
}

// normalizeAddress returns addr with the passed default port appended if
// there is not already a port specified.
func normalizeAddress(addr, defaultPort string) string {
	_, _, err := net.SplitHostPort(addr)
	if err != nil {
		return net.JoinHostPort(addr, defaultPort)
	}
	return addr
}

// normalizeAddresses returns a new slice with all the passed peer addresses
// normalized with the given default port, and all duplicates removed.
func normalizeAddresses(addrs []string, defaultPort string) []string {
	for i, addr := range addrs {
		addrs[i] = normalizeAddress(addr, defaultPort)
	}

	return removeDuplicateAddresses(addrs)
}

// filesExists reports whether the named file or directory exists.
func fileExists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// newConfigParser returns a new command line flags parser.
func newConfigParser(cfg *config, so *serviceOptions, options flags.Options) *flags.Parser {
	parser := flags.NewParser(cfg, options)
	if runtime.GOOS == "windows" {
		parser.AddGroup("Service Options", "Service Options", so)
	}
	return parser
}

// parseAndValidateAPIVersions parses a string containing comma-separated API
// versions, validates them and returns a slice of integer versions.
func parseAndValidateAPIVersions(vs string) ([]uint, error) {
	versions := strings.Split(vs, ",")
	parsed := make([]uint, 0, len(versions))

	// Validate out of bounds config
	if len(versions) == 0 || len(versions) > 2 {
		return nil, fmt.Errorf("invalid API versions config," +
			"must have at least one and at most two")
	}

	for _, v := range versions {
		// Convert to integer
		conv, err := strconv.Atoi(v)
		if err != nil {
			return nil, err
		}
		// Validate that version exists
		switch conv {
		case v1.APIVersion:
		case v2.APIVersion:
		default:
			return nil, fmt.Errorf("%s is an invalid API version,"+
				"must be 1, 2 or both", v)
		}
		parsed = append(parsed, uint(conv))
	}

	return parsed, nil
}

// loadConfig initializes and parses the config using a config file and command
// line options.
//
// The configuration proceeds as follows:
//  1. Start with a default config with sane settings
//  2. Pre-parse the command line to check for an alternative config file
//  3. Load configuration file overwriting defaults with any specified options
//  4. Parse CLI options and overwrite/add any specified options
//
// The above results in daemon functioning properly without any config settings
// while still allowing the user to override settings with config files and
// command line options.  Command line options always take precedence.
func loadConfig() (*config, []string, error) {
	// Default config.
	cfg := config{
		HomeDir:       defaultHomeDir,
		ConfigFile:    defaultConfigFile,
		DebugLevel:    defaultLogLevel,
		DataDir:       defaultDataDir,
		LogDir:        defaultLogDir,
		HTTPSKey:      defaultHTTPSKeyFile,
		HTTPSCert:     defaultHTTPSCertFile,
		Version:       version(),
		APIVersions:   defaultAPIVersions,
		Confirmations: int32(defaultConfirmations),
		MaxDigests:    int32(defaultMaxDigests),
	}

	// Service options which are only added on Windows.
	serviceOpts := serviceOptions{}

	// Pre-parse the command line options to see if an alternative config
	// file or the version flag was specified.  Any errors aside from the
	// help message error can be ignored here since they will be caught by
	// the final parse below.
	preCfg := cfg
	preParser := newConfigParser(&preCfg, &serviceOpts, flags.HelpFlag)
	_, err := preParser.Parse()
	if err != nil {
		if e, ok := err.(*flags.Error); ok && e.Type == flags.ErrHelp {
			fmt.Fprintln(os.Stderr, err)
			return nil, nil, err
		}
	}

	// Show the version and exit if the version flag was specified.
	appName := filepath.Base(os.Args[0])
	appName = strings.TrimSuffix(appName, filepath.Ext(appName))
	usageMessage := fmt.Sprintf("Use %s -h to show usage", appName)
	if preCfg.ShowVersion {
		fmt.Println(appName, "version", version())
		os.Exit(0)
	}

	// Perform service command and exit if specified.  Invalid service
	// commands show an appropriate error.  Only runs on Windows since
	// the runServiceCommand function will be nil when not on Windows.
	if serviceOpts.ServiceCommand != "" && runServiceCommand != nil {
		err := runServiceCommand(serviceOpts.ServiceCommand)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(0)
	}

	// Update the home directory for stakepoold if specified. Since the
	// home directory is updated, other variables need to be updated to
	// reflect the new changes.
	if preCfg.HomeDir != "" {
		cfg.HomeDir, _ = filepath.Abs(preCfg.HomeDir)

		if preCfg.ConfigFile == defaultConfigFile {
			cfg.ConfigFile = filepath.Join(cfg.HomeDir, defaultConfigFilename)
		} else {
			cfg.ConfigFile = preCfg.ConfigFile
		}
		if preCfg.DataDir == defaultDataDir {
			cfg.DataDir = filepath.Join(cfg.HomeDir, defaultDataDirname)
		} else {
			cfg.DataDir = preCfg.DataDir
		}
		if preCfg.HTTPSKey == defaultHTTPSKeyFile {
			cfg.HTTPSKey = filepath.Join(cfg.HomeDir, "https.key")
		} else {
			cfg.HTTPSKey = preCfg.HTTPSKey
		}
		if preCfg.HTTPSCert == defaultHTTPSCertFile {
			cfg.HTTPSCert = filepath.Join(cfg.HomeDir, "https.cert")
		} else {
			cfg.HTTPSCert = preCfg.HTTPSCert
		}
		if preCfg.LogDir == defaultLogDir {
			cfg.LogDir = filepath.Join(cfg.HomeDir, defaultLogDirname)
		} else {
			cfg.LogDir = preCfg.LogDir
		}
	}

	// Load additional config from file.
	var configFileError error
	parser := newConfigParser(&cfg, &serviceOpts, flags.Default)
	if !(preCfg.SimNet) || cfg.ConfigFile != defaultConfigFile {
		err := flags.NewIniParser(parser).ParseFile(cfg.ConfigFile)
		if err != nil {
			if _, ok := err.(*os.PathError); !ok {
				fmt.Fprintf(os.Stderr, "Error parsing config "+
					"file: %v\n", err)
				fmt.Fprintln(os.Stderr, usageMessage)
				return nil, nil, err
			}
			configFileError = err
		}
	}

	// Parse command line options again to ensure they take precedence.
	remainingArgs, err := parser.Parse()
	if err != nil {
		if e, ok := err.(*flags.Error); !ok || e.Type != flags.ErrHelp {
			fmt.Fprintln(os.Stderr, usageMessage)
		}
		return nil, nil, err
	}

	// Create the home directory if it doesn't already exist.
	funcName := "loadConfig"
	err = os.MkdirAll(defaultHomeDir, 0700)
	if err != nil {
		// Show a nicer error message if it's because a symlink is
		// linked to a directory that does not exist (probably because
		// it's not mounted).
		if e, ok := err.(*os.PathError); ok && os.IsExist(err) {
			if link, lerr := os.Readlink(e.Path); lerr == nil {
				str := "is symlink %s -> %s mounted?"
				err = fmt.Errorf(str, e.Path, link)
			}
		}

		str := "%s: Failed to create home directory: %v"
		err := fmt.Errorf(str, funcName, err)
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, err
	}

	// Multiple networks can't be selected simultaneously.
	numNets := 0

	// Count number of network flags passed; assign active network params
	// while we're at it
	port := defaultMainnetPort
	activeNetParams = &mainNetParams
	if cfg.TestNet {
		numNets++
		activeNetParams = &testNet3Params
		port = defaultTestnetPort
	}
	if cfg.SimNet {
		numNets++
		// Also disable dns seeding on the simulation test network.
		activeNetParams = &simNetParams
	}
	if numNets > 1 {
		str := "%s: The testnet and simnet params can't be " +
			"used together -- choose one of the three"
		err := fmt.Errorf(str, funcName)
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usageMessage)
		return nil, nil, err
	}

	// Append the network type to the data directory so it is "namespaced"
	// per network.  In addition to the block database, there are other
	// pieces of data that are saved to disk such as address manager state.
	// All data is specific to a network, so namespacing the data directory
	// means each individual piece of serialized data does not have to
	// worry about changing names per network and such.
	cfg.DataDir = cleanAndExpandPath(cfg.DataDir)
	cfg.DataDir = filepath.Join(cfg.DataDir, netName(activeNetParams))

	// Append the network type to the log directory so it is "namespaced"
	// per network in the same fashion as the data directory.
	cfg.LogDir = cleanAndExpandPath(cfg.LogDir)
	cfg.LogDir = filepath.Join(cfg.LogDir, netName(activeNetParams))

	// Special show command to list supported subsystems and exit.
	if cfg.DebugLevel == "show" {
		fmt.Println("Supported subsystems", supportedSubsystems())
		os.Exit(0)
	}

	// Initialize log rotation.  After log rotation has been initialized, the
	// logger variables may be used.
	initLogRotator(filepath.Join(cfg.LogDir, defaultLogFilename))

	// Parse, validate, and set debug log level(s).
	if err := parseAndSetDebugLevels(cfg.DebugLevel); err != nil {
		err := fmt.Errorf("%s: %v", funcName, err.Error())
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usageMessage)
		return nil, nil, err
	}

	// Validate profile port number
	if cfg.Profile != "" {
		profilePort, err := strconv.Atoi(cfg.Profile)
		if err != nil || profilePort < 1024 || profilePort > 65535 {
			str := "%s: The profile port must be between 1024 and 65535"
			err := fmt.Errorf(str, funcName)
			fmt.Fprintln(os.Stderr, err)
			fmt.Fprintln(os.Stderr, usageMessage)
			return nil, nil, err
		}
	}

	// Validate API versions from config
	_, err = parseAndValidateAPIVersions(cfg.APIVersions)
	if err != nil {
		return nil, nil, err
	}

	// Add the default listener if none were specified. The default
	// listener is all addresses on the listen port for the network
	// we are to connect to.
	if len(cfg.Listeners) == 0 {
		cfg.Listeners = []string{
			net.JoinHostPort("", port),
		}
	}

	// Add default port to all listener addresses if needed and remove
	// duplicate addresses.
	cfg.Listeners = normalizeAddresses(cfg.Listeners, port)

	if len(cfg.WalletHost) == 0 && len(cfg.StoreHost) == 0 {
		str := "%s: wallethost is not set in config"
		err := fmt.Errorf(str, funcName)
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, err
	}

	if len(cfg.WalletCert) == 0 && len(cfg.StoreHost) == 0 {
		str := "%s: walletcert is not set in config"
		err := fmt.Errorf(str, funcName)
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, err
	}

	if len(cfg.StoreHost) != 0 {
		cfg.StoreHost = normalizeAddress(cfg.StoreHost, port)
		cfg.StoreCert = cleanAndExpandPath(cfg.StoreCert)
	}

	// Add default wallet port for the active network if there's no port specified
	cfg.WalletHost = normalizeAddress(cfg.WalletHost,
		activeNetParams.WalletRPCServerPort)
	cfg.WalletCert = cleanAndExpandPath(cfg.WalletCert)

	if len(cfg.StoreHost) == 0 && !fileExists(cfg.WalletCert) {
		path := filepath.Join(cfg.HomeDir, cfg.WalletCert)
		if !fileExists(path) {
			str := "%s: walletcert " + cfg.WalletCert + " and " +
				path + " don't exist"
			err := fmt.Errorf(str, funcName)
			fmt.Fprintln(os.Stderr, err)
			return nil, nil, err
		}

		cfg.WalletCert = path
	}

	// Set path for the client key/cert depending on if they are set in options
	if cfg.WalletClientCert == "" {
		cfg.WalletClientCert = filepath.Join(cfg.HomeDir, walletClientCertFile)
	}
	if cfg.WalletClientKey == "" {
		cfg.WalletClientKey = filepath.Join(cfg.HomeDir, walletClientKeyFile)
	}

	if len(cfg.StoreHost) == 0 {
		if len(cfg.APITokens) == 0 {
			err := fmt.Errorf("%s: At least one apitoken is required when "+
				"running in backend mode", funcName)
			return nil, nil, err
		}

		var validTokens []string
		for _, token := range cfg.APITokens {
			token = strings.TrimSpace(token)
			if len(token) > 0 {
				validTokens = append(validTokens, token)
				continue
			}

			err := fmt.Errorf("%s: Blank apitoken found -- ensure all "+
				"apitoken values are not blank", funcName)
			return nil, nil, err
		}
		cfg.APITokens = validTokens
	}

	// Warn about missing config file only after all other configuration is
	// done.  This prevents the warning on help messages and invalid
	// options.  Note this should go directly before the return.
	if configFileError != nil {
		log.Warnf("%v", configFileError)
	}

	return &cfg, remainingArgs, nil
}
