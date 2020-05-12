// Copyright © 2020 Karim Radhouani <medkarimrdi@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh/terminal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	defaultGrpcPort = "57400"
)

var cfgFile string
var f io.WriteCloser

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "gnmiClient",
	Short: "run gnmi rpcs from the terminal",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if viper.GetBool("nolog") {
			f = myWriteCloser{}
			return
		}
		if viper.GetBool("logstdout") {
			log.SetFlags(log.LstdFlags | log.Lmicroseconds)
			f = os.Stdout
			return
		}
		var err error
		logFile := viper.GetString("log-file")
		if logFile == "" {
			logFile = fmt.Sprintf("%s/.gnmi/gnmiClient.log", os.Getenv("HOME"))
			viper.Set("log-file", logFile)
		}
		if err = os.MkdirAll(filepath.Dir(logFile), 0755); err != nil {
			fmt.Printf("could not create log directory '%s':%v\n", filepath.Dir(logFile), err)
			return
		}
		f, err = os.OpenFile(viper.GetString("log-file"), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			log.Fatalf("error opening file: %v", err)
		}
		log.SetFlags(log.LstdFlags | log.Lmicroseconds)
		log.SetOutput(f)
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if !viper.GetBool("nolog") && !viper.GetBool("logstdout") {
			f.Close()
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.gnmiClient.yaml)")
	rootCmd.PersistentFlags().StringSliceP("address", "a", []string{}, "comma separated gnmi targets addresses")
	rootCmd.PersistentFlags().StringP("username", "u", "", "username")
	rootCmd.PersistentFlags().StringP("password", "p", "", "password")
	rootCmd.PersistentFlags().StringP("encoding", "e", "JSON", "one of: JSON, BYTES, PROTO, ASCII, JSON_IETF.")
	rootCmd.PersistentFlags().BoolP("insecure", "", false, "insecure connection")
	rootCmd.PersistentFlags().StringP("tls-ca", "", "", "tls certificate authority")
	rootCmd.PersistentFlags().StringP("tls-cert", "", "", "tls certificate")
	rootCmd.PersistentFlags().StringP("tls-key", "", "", "tls key")
	rootCmd.PersistentFlags().StringP("timeout", "", "30s", "grpc timeout")
	rootCmd.PersistentFlags().BoolP("debug", "d", false, "debug mode")
	rootCmd.PersistentFlags().BoolP("skip-verify", "", false, "skip verify tls connection")
	rootCmd.PersistentFlags().BoolP("no-prefix", "", false, "do not add [ip:port] prefix to print output in case of multiple targets")
	rootCmd.PersistentFlags().BoolP("proxy-from-env", "", false, "use proxy from environment")
	rootCmd.PersistentFlags().BoolP("raw", "", false, "output messages as received")
	rootCmd.PersistentFlags().StringP("log-file", "", "", "log file path")
	rootCmd.PersistentFlags().BoolP("nolog", "", false, "do not generate logs")
	rootCmd.PersistentFlags().BoolP("logstdout", "", false, "log to stdout")
	rootCmd.PersistentFlags().IntP("max-msg-size", "", 512, "max tls msg size")

	//
	viper.BindPFlag("address", rootCmd.PersistentFlags().Lookup("address"))
	viper.BindPFlag("username", rootCmd.PersistentFlags().Lookup("username"))
	viper.BindPFlag("password", rootCmd.PersistentFlags().Lookup("password"))
	viper.BindPFlag("encoding", rootCmd.PersistentFlags().Lookup("encoding"))
	viper.BindPFlag("insecure", rootCmd.PersistentFlags().Lookup("insecure"))
	viper.BindPFlag("tls-ca", rootCmd.PersistentFlags().Lookup("tls-ca"))
	viper.BindPFlag("tls-cert", rootCmd.PersistentFlags().Lookup("tls-cert"))
	viper.BindPFlag("tls-key", rootCmd.PersistentFlags().Lookup("tls-key"))
	viper.BindPFlag("timeout", rootCmd.PersistentFlags().Lookup("timeout"))
	viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))
	viper.BindPFlag("skip-verify", rootCmd.PersistentFlags().Lookup("skip-verify"))
	viper.BindPFlag("no-prefix", rootCmd.PersistentFlags().Lookup("no-prefix"))
	viper.BindPFlag("proxy-from-env", rootCmd.PersistentFlags().Lookup("proxy-from-env"))
	viper.BindPFlag("raw", rootCmd.PersistentFlags().Lookup("raw"))
	viper.BindPFlag("log-file", rootCmd.PersistentFlags().Lookup("log-file"))
	viper.BindPFlag("nolog", rootCmd.PersistentFlags().Lookup("nolog"))
	viper.BindPFlag("logstdout", rootCmd.PersistentFlags().Lookup("logstdout"))
	viper.BindPFlag("max-msg-size", rootCmd.PersistentFlags().Lookup("max-msg-size"))
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		// Search config in home directory with name ".gnmiClient" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigName(".gnmiClient")
	}

	//viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	viper.ReadInConfig()
}
func readUsername() (string, error) {
	var username string
	fmt.Print("username: ")
	_, err := fmt.Scan(&username)
	if err != nil {
		return "", err
	}
	return username, nil
}
func readPassword() (string, error) {
	fmt.Print("password: ")
	pass, err := terminal.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	fmt.Println()
	return string(pass), nil
}
func createGrpcConn(address string) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{}
	timeout, err := time.ParseDuration(viper.GetString("timeout"))
	if err != nil {
		return nil, err
	}
	opts = append(opts, grpc.WithTimeout(timeout))
	opts = append(opts, grpc.WithBlock())
	if viper.GetInt("max-msg-size") > 0 {
		opts = append(opts, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(viper.GetInt("max-msg-size"))))
	}
	if !viper.GetBool("proxy-from-env") {
		opts = append(opts, grpc.WithNoProxy())
	}
	if viper.GetBool("insecure") {
		opts = append(opts, grpc.WithInsecure())
	} else {
		tlsConfig := &tls.Config{
			Renegotiation:      tls.RenegotiateNever,
			InsecureSkipVerify: viper.GetBool("skip-verify"),
		}
		certificates, err := loadCerts()
		if err != nil {
			log.Printf("failed loading certificates: %v", err)
		}
		tlsConfig.Certificates = certificates

		certPool, err := loadCACerts()
		if err != nil {
			log.Printf("failed loading CA certificates: %v", err)
		}
		tlsConfig.RootCAs = certPool
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	}
	conn, err := grpc.Dial(address, opts...)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
func gnmiPathToXPath(p *gnmi.Path) string {
	if p == nil {
		return ""
	}
	pathElems := make([]string, 0, len(p.GetElem()))
	for _, pe := range p.GetElem() {
		elem := ""
		if pe.GetName() != "" {
			elem += pe.GetName()
		}
		if pe.GetKey() != nil {
			for k, v := range pe.GetKey() {
				elem += fmt.Sprintf("[%s=%s]", k, v)
			}
		}
		pathElems = append(pathElems, elem)
	}
	return strings.Join(pathElems, "/")
}
func loadCerts() ([]tls.Certificate, error) {
	tlsCert := viper.GetString("tls-cert")
	tlsKey := viper.GetString("tls-key")
	var certificate tls.Certificate
	var err error
	if tlsCert != "" && tlsKey != "" {
		certificate, err = tls.LoadX509KeyPair(tlsCert, tlsKey)
		if err != nil {
			return nil, err
		}
	}
	return []tls.Certificate{certificate}, nil
}
func loadCACerts() (*x509.CertPool, error) {
	tlsCa := viper.GetString("tls-ca")
	certPool := x509.NewCertPool()
	if tlsCa != "" {
		caFile, err := ioutil.ReadFile(tlsCa)
		if err != nil {
			return nil, err
		}

		if ok := certPool.AppendCertsFromPEM(caFile); !ok {
			return nil, errors.New("failed to append certificate")
		}
	}
	return certPool, nil
}
func printer(ctx context.Context, c chan string) {
	for {
		select {
		case m := <-c:
			fmt.Println(m)
		case <-ctx.Done():
			return
		}
	}
}
func gather(ctx context.Context, c chan string, ls *[]string) {
	for {
		select {
		case m := <-c:
			*ls = append(*ls, m)
		case <-ctx.Done():
			return
		}
	}
}

func getValue(updValue *gnmi.TypedValue) (interface{}, error) {
	var value interface{}
	var jsondata []byte
	switch updValue.Value.(type) {
	case *gnmi.TypedValue_AsciiVal:
		value = updValue.GetAsciiVal()
	case *gnmi.TypedValue_BoolVal:
		value = updValue.GetBoolVal()
	case *gnmi.TypedValue_BytesVal:
		value = updValue.GetBytesVal()
	case *gnmi.TypedValue_DecimalVal:
		value = updValue.GetDecimalVal()
	case *gnmi.TypedValue_FloatVal:
		value = updValue.GetFloatVal()
	case *gnmi.TypedValue_IntVal:
		value = updValue.GetIntVal()
	case *gnmi.TypedValue_StringVal:
		value = updValue.GetStringVal()
	case *gnmi.TypedValue_UintVal:
		value = updValue.GetUintVal()
	case *gnmi.TypedValue_JsonIetfVal:
		jsondata = updValue.GetJsonIetfVal()
	case *gnmi.TypedValue_JsonVal:
		jsondata = updValue.GetJsonVal()
	case *gnmi.TypedValue_LeaflistVal:
		value = updValue.GetLeaflistVal()
	case *gnmi.TypedValue_ProtoBytes:
		value = updValue.GetProtoBytes()
	case *gnmi.TypedValue_AnyVal:
		value = updValue.GetAnyVal()
	}
	if value == nil {
		err := json.Unmarshal(jsondata, &value)
		if err != nil {
			return nil, err
		}
	}
	return value, nil
}

type myWriteCloser struct {
	io.Writer
}

func (myWriteCloser) Close() error {
	return nil
}
