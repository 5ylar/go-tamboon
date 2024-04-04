package main

import (
	"context"
	"fmt"
	"go-tamboon/conf"
	"go-tamboon/donate"
	"os"
	"os/signal"
	"syscall"

	"github.com/omise/omise-go"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := conf.NewConf()

	if err != nil {
		panic(err)
	}

	omiseClient, err := omise.NewClient(cfg.OmisePublicKey, cfg.OmiseSecretKey)

	if err != nil {
		panic(err)
	}

	omiseClient.WithContext(ctx)

	filepath := os.Args[1]

	fileReader, err := os.Open(filepath)

	if err != nil {
		panic(err)
	}

	go func() {
		signalCh := make(chan os.Signal, 1)
		signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
		<-signalCh

		cancel()
	}()

	donation := donate.NewDonation(omiseClient)

	donators, err := donation.DecryptDonators(fileReader)

	if err != nil {
		panic(err)
	}

	summary, err := donation.Donate(ctx, 3, donators)

	if err != nil {
		panic(err)
	}

	top3Donators := []string{"", "", ""}

	for i, donator := range summary.Top3Donators {
		top3Donators[i] = donator.Name
	}

	fmt.Printf(
		`
            total received: THB  %.2f
      successfully donated: THB  %.2f
           faulty donation: THB  %.2f

        average per person: THB  %.2f
                top donors: %s
                            %s
                            %s
        `,
		float64(summary.TotalReceivedAmount)/100.00,
		float64(summary.TotalSuccessDonatedAmount)/100.00,
		float64(summary.TotalFailedDonatedAmount)/100.00,
		float64(summary.AVGPerPersonAmount)/100.00,
		top3Donators[0],
		top3Donators[1],
		top3Donators[2],
	)
}
