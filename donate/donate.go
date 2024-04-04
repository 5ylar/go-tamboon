package donate

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"go-tamboon/cipher"
	"io"
	"log"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/omise/omise-go"
	"github.com/omise/omise-go/operations"
)

type DonationSummary struct {
	TotalReceivedAmount       int64
	TotalSuccessDonatedAmount int64
	TotalFailedDonatedAmount  int64
	AVGPerPersonAmount        int64 // only success donated
	Top3Donators              []Donator
}

type Donator struct {
	Name           string
	AmountSubunits int64
	CCNumber       string
	CVV            string
	ExpMonth       int16
	ExpYear        int16
}

type Donation struct {
	omiseClient *omise.Client
}

func NewDonation(omiseClient *omise.Client) *Donation {
	return &Donation{omiseClient}
}

func (d *Donation) DecryptDonators(fileReader io.Reader) ([]Donator, error) {
	r, err := cipher.NewRot128Reader(fileReader)

	if err != nil {
		return nil, err
	}

	var csvContent []byte

	for {
		chunk := make([]byte, 512)

		bc, err := r.Read(chunk)

		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, err
		}

		if bc <= 0 {
			break
		}

		csvContent = append(csvContent, chunk...)
	}

	var donators []Donator

	cr := csv.NewReader(bytes.NewReader(csvContent))

	// for header row
	_, err = cr.Read()

	if err != nil {
		return nil, err
	}

	for {
		rec, err := cr.Read()

		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, csv.ErrFieldCount) {
				break
			}

			return nil, err
		}

		if len(rec) != 6 {
			return nil, errors.New("wrong csv format")
		}

		amount, err := strconv.ParseInt(rec[1], 10, 64)

		if err != nil {
			return nil, errors.New("wrong amount input")
		}

		expMonth, err := strconv.ParseInt(rec[4], 10, 16)

		if err != nil {
			return nil, errors.New("wrong exp month input")
		}

		expYear, err := strconv.ParseInt(rec[5], 10, 16)

		if err != nil {
			return nil, errors.New("wrong exp year input")
		}

		donators = append(donators, Donator{
			Name:           rec[0],
			AmountSubunits: amount,
			CCNumber:       rec[2],
			CVV:            rec[3],
			ExpMonth:       int16(expMonth),
			ExpYear:        int16(expYear),
		})
	}

	log.Println("[INFO] total donators", len(donators))

	return donators, nil
}

func (d *Donation) Donate(ctx context.Context, concurrent int, donators []Donator) (DonationSummary, error) {

	rateLimitCh := make(chan struct{}, concurrent)
	donatorsCh := make(chan Donator, concurrent)

	var hasCancelledSignal bool

	go func() {
		<-ctx.Done()

		hasCancelledSignal = true

		log.Println("[WARN] donation cancelling")
		time.Sleep(time.Second * 2)

		close(donatorsCh)
		close(rateLimitCh)
	}()

	var wg sync.WaitGroup

	go func() {
		for _, donator := range donators {
			if hasCancelledSignal {
				return // don't need to wait or close channels
			}

			donatorsCh <- donator
			rateLimitCh <- struct{}{}
			wg.Add(1)
		}

		wg.Wait()

		log.Println("done")

		close(donatorsCh)
		close(rateLimitCh)
	}()

	successDonatorsCh := make(chan Donator, 10)
	failedDonatorsCh := make(chan Donator, 10)

	var (
		totalReceivedAmount       atomic.Int64
		totalSuccessDonatedAmount int64
		totalFailedDonatedAmount  int64
		totalSuccessDonated       int64
		top3Donators              []Donator
	)

	go func() {
		for donator := range successDonatorsCh {
			log.Println("[INFO] successfully donated", donator.Name, donator.AmountSubunits)
			totalSuccessDonated += 1
			totalReceivedAmount.Add(donator.AmountSubunits)
			totalSuccessDonatedAmount += donator.AmountSubunits

			top3Donators = append(top3Donators, donator)

			sort.Slice(top3Donators, func(i, j int) bool {
				return top3Donators[i].AmountSubunits > top3Donators[j].AmountSubunits
			})

			if len(top3Donators) > 3 {
				top3Donators = top3Donators[:3]
			}
		}
	}()

	go func() {
		for donator := range failedDonatorsCh {
			log.Println("[ERROR] failed donated", donator.Name, donator.AmountSubunits, donator)
			totalReceivedAmount.Add(donator.AmountSubunits)
			totalFailedDonatedAmount += donator.AmountSubunits
		}
	}()

	for donator := range donatorsCh {
		go func(donator Donator) {
			defer func() {
				time.Sleep(time.Second * 2)
				<-rateLimitCh
				wg.Done()
			}()

			var token omise.Token

			err := d.omiseClient.Do(&token, &operations.CreateToken{
				Name:            donator.Name,
				Number:          donator.CCNumber,
				ExpirationMonth: time.Month(donator.ExpMonth),
				ExpirationYear:  int(donator.ExpYear),
				SecurityCode:    donator.CVV,
			})

			if err != nil {
				log.Println("[ERROR] failed to create token", err)
				failedDonatorsCh <- donator
				return
			}

			var charge omise.Charge

			err = d.omiseClient.Do(&charge, &operations.CreateCharge{
				Amount:   donator.AmountSubunits,
				Currency: "THB",
				Card:     token.ID,
			})

			if err != nil {
				log.Println("[ERROR] failed to create charge", err)
				failedDonatorsCh <- donator
				return
			}

			successDonatorsCh <- donator
		}(donator)
	}

	close(successDonatorsCh)
	close(failedDonatorsCh)

	summary := DonationSummary{
		TotalReceivedAmount:       totalReceivedAmount.Load(),
		TotalSuccessDonatedAmount: totalSuccessDonatedAmount,
		TotalFailedDonatedAmount:  totalFailedDonatedAmount,
		Top3Donators:              top3Donators,
	}

	if totalSuccessDonated > 0 {
		summary.AVGPerPersonAmount = totalSuccessDonatedAmount / totalSuccessDonated
	}

	return summary, nil
}
