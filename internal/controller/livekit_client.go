package controller

import (
	"context"
	"fmt"

	lksdk "github.com/livekit/server-sdk-go/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sipv1alpha1 "github.com/adrijh/livekit-sip-operator/api/v1alpha1"
)

// resolveLiveKitClient builds a SIPClient from the LivekitReference.
// The URL is constructed from the service name and port; API credentials
// are read from the referenced Secret.
func resolveLiveKitClient(
	ctx context.Context,
	c client.Client,
	namespace string,
	ref sipv1alpha1.LivekitReference,
) (*lksdk.SIPClient, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Namespace: namespace,
		Name:      ref.SecretName,
	}

	if err := c.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("reading LiveKit credentials secret %q: %w", key, err)
	}

	apiKey := string(secret.Data["LIVEKIT_API_KEY"])
	apiSecret := string(secret.Data["LIVEKIT_API_SECRET"])

	if apiKey == "" || apiSecret == "" {
		return nil, fmt.Errorf("LiveKit credentials secret %q missing required keys (LIVEKIT_API_KEY, LIVEKIT_API_SECRET)", key)
	}

	port := ref.Port
	if port == 0 {
		port = 7880
	}
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", ref.Service, namespace, port)

	return lksdk.NewSIPClient(url, apiKey, apiSecret), nil
}
