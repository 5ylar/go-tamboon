package conf

import (
	"github.com/kelseyhightower/envconfig"
)

type Conf struct {
    OmisePublicKey string `envconfig:"OMISE_PUBLIC_KEY" required:"true"`
	OmiseSecretKey string `envconfig:"OMISE_SECRET_KEY" required:"true"`
}

func NewConf() (*Conf, error) {
	var conf Conf

	err := envconfig.Process("", &conf)

	if err != nil {
		return nil, err
	}

	return &conf, nil
}
