package reachymini_test

import (
	"context"
	"fmt"
	"log"
	"time"

	reachymini "github.com/nlm/reachy-mini-sdk-go"
)

// ExampleClient_WakeUp shows the minimal sequence to move the robot at all:
// motor control must be explicitly enabled before any movement command has
// an effect (see EnsureMotorMode's doc comment).
func ExampleClient_WakeUp() {
	client := reachymini.New("http://localhost:8000")
	ctx := context.Background()

	if _, err := client.EnsureMotorMode(ctx, reachymini.MotorModeEnabled); err != nil {
		log.Fatal(err)
	}
	if _, err := client.WakeUp(ctx); err != nil {
		log.Fatal(err)
	}
}

// ExampleClient_Goto smoothly moves the head to a target pose over one
// second, using min-jerk interpolation for a natural-looking motion.
func ExampleClient_Goto() {
	client := reachymini.New("http://localhost:8000")
	ctx := context.Background()

	_, err := client.Goto(ctx, reachymini.GotoRequest{
		HeadPose:      reachymini.NewXYZRPYPose(0, 0, 0, 0, 0.2, 0),
		Duration:      1.0,
		Interpolation: reachymini.InterpolationMinJerk,
	})
	if err != nil {
		log.Fatal(err)
	}
}

// ExampleClient_StreamFullState prints the robot's head pose as it changes,
// until ctx is cancelled.
func ExampleClient_StreamFullState() {
	client := reachymini.New("http://localhost:8000")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	states, errs, err := client.StreamFullState(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for {
		select {
		case state, ok := <-states:
			if !ok {
				return
			}
			fmt.Println(state.HeadPose)
		case err := <-errs:
			log.Fatal(err)
		}
	}
}

// ExampleClient_PlaySound plays a sound file already uploaded to the robot
// (see Client.UploadSound and Client.ListSounds).
func ExampleClient_PlaySound() {
	client := reachymini.New("http://localhost:8000")
	ctx := context.Background()

	if err := client.PlaySound(ctx, "boot_chime.wav"); err != nil {
		log.Fatal(err)
	}
}
