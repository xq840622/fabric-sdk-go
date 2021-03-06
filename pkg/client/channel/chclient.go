/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package channel enables access to a channel on a Fabric network.
package channel

import (
	reqContext "context"
	"time"

	"github.com/hyperledger/fabric-sdk-go/pkg/client/channel/invoke"
	"github.com/hyperledger/fabric-sdk-go/pkg/client/common/discovery/greylist"
	"github.com/hyperledger/fabric-sdk-go/pkg/common/errors/retry"
	"github.com/hyperledger/fabric-sdk-go/pkg/common/errors/status"
	"github.com/hyperledger/fabric-sdk-go/pkg/common/providers/context"
	"github.com/hyperledger/fabric-sdk-go/pkg/common/providers/fab"
	contextImpl "github.com/hyperledger/fabric-sdk-go/pkg/context"
	"github.com/pkg/errors"
)

// Client enables access to a channel on a Fabric network.
//
// A channel client instance provides a handler to interact with peers on specified channel.
// An application that requires interaction with multiple channels should create a separate
// instance of the channel client for each channel. Channel client supports non-admin functions only.
type Client struct {
	context      context.Channel
	membership   fab.ChannelMembership
	eventService fab.EventService
	greylist     *greylist.Filter
}

// ClientOption describes a functional parameter for the New constructor
type ClientOption func(*Client) error

// New returns a Client instance.
func New(channelProvider context.ChannelProvider, opts ...ClientOption) (*Client, error) {

	channelContext, err := channelProvider()
	if err != nil {
		return nil, errors.WithMessage(err, "failed to create channel context")
	}

	greylistProvider := greylist.New(channelContext.EndpointConfig().TimeoutOrDefault(fab.DiscoveryGreylistExpiry))

	if channelContext.ChannelService() == nil {
		return nil, errors.New("channel service not initialized")
	}

	eventService, err := channelContext.ChannelService().EventService()
	if err != nil {
		return nil, errors.WithMessage(err, "event service creation failed")
	}

	membership, err := channelContext.ChannelService().Membership()
	if err != nil {
		return nil, errors.WithMessage(err, "membership creation failed")
	}

	channelClient := Client{
		membership:   membership,
		eventService: eventService,
		greylist:     greylistProvider,
		context:      channelContext,
	}

	for _, param := range opts {
		err := param(&channelClient)
		if err != nil {
			return nil, errors.WithMessage(err, "option failed")
		}
	}

	return &channelClient, nil
}

// Query chaincode using request and optional options provided
func (cc *Client) Query(request Request, options ...RequestOption) (Response, error) {
	optsWithTimeout, err := cc.addDefaultTimeout(cc.context, fab.Query, options...)
	if err != nil {
		return Response{}, errors.WithMessage(err, "option failed")
	}

	return cc.InvokeHandler(invoke.NewQueryHandler(), request, optsWithTimeout...)
}

// Execute prepares and executes transaction using request and optional options provided
func (cc *Client) Execute(request Request, options ...RequestOption) (Response, error) {
	optsWithTimeout, err := cc.addDefaultTimeout(cc.context, fab.Execute, options...)
	if err != nil {
		return Response{}, errors.WithMessage(err, "option failed")
	}

	return cc.InvokeHandler(invoke.NewExecuteHandler(), request, optsWithTimeout...)
}

//InvokeHandler invokes handler using request and options provided
func (cc *Client) InvokeHandler(handler invoke.Handler, request Request, options ...RequestOption) (Response, error) {
	//Read execute tx options
	txnOpts, err := cc.prepareOptsFromOptions(cc.context, options...)
	if err != nil {
		return Response{}, err
	}

	reqCtx, cancel := cc.createReqContext(&txnOpts)
	defer cancel()

	//Prepare context objects for handler
	requestContext, clientContext, err := cc.prepareHandlerContexts(reqCtx, request, txnOpts)
	if err != nil {
		return Response{}, err
	}

	invoker := retry.NewInvoker(
		requestContext.RetryHandler,
		retry.WithBeforeRetry(
			func(err error) {
				cc.greylist.Greylist(err)

				// Reset context parameters
				requestContext.Opts.Targets = txnOpts.Targets
				requestContext.Error = nil
				requestContext.Response = invoke.Response{}
			},
		),
	)

	complete := make(chan bool)
	go func() {
		_, _ = invoker.Invoke(
			func() (interface{}, error) {
				handler.Handle(requestContext, clientContext)
				return nil, requestContext.Error
			})
		complete <- true
	}()
	select {
	case <-complete:
		return Response(requestContext.Response), requestContext.Error
	case <-reqCtx.Done():
		return Response{}, status.New(status.ClientStatus, status.Timeout.ToInt32(),
			"request timed out or been cancelled", nil)
	}
}

//createReqContext creates req context for invoke handler
func (cc *Client) createReqContext(txnOpts *requestOptions) (reqContext.Context, reqContext.CancelFunc) {

	if txnOpts.Timeouts == nil {
		txnOpts.Timeouts = make(map[fab.TimeoutType]time.Duration)
	}

	//setting default timeouts when not provided
	if txnOpts.Timeouts[fab.Execute] == 0 {
		txnOpts.Timeouts[fab.Execute] = cc.context.EndpointConfig().TimeoutOrDefault(fab.Execute)
	}

	reqCtx, cancel := contextImpl.NewRequest(cc.context, contextImpl.WithTimeout(txnOpts.Timeouts[fab.Execute]),
		contextImpl.WithParent(txnOpts.ParentContext))
	//Add timeout overrides here as a value so that it can be used by immediate child contexts (in handlers/transactors)
	reqCtx = reqContext.WithValue(reqCtx, contextImpl.ReqContextTimeoutOverrides, txnOpts.Timeouts)

	return reqCtx, cancel
}

//prepareHandlerContexts prepares context objects for handlers
func (cc *Client) prepareHandlerContexts(reqCtx reqContext.Context, request Request, o requestOptions) (*invoke.RequestContext, *invoke.ClientContext, error) {

	if request.ChaincodeID == "" || request.Fcn == "" {
		return nil, nil, errors.New("ChaincodeID and Fcn are required")
	}

	chConfig, err := cc.context.ChannelService().ChannelConfig()
	if err != nil {
		return nil, nil, errors.WithMessage(err, "failed to retrieve channel config")
	}
	transactor, err := cc.context.InfraProvider().CreateChannelTransactor(reqCtx, chConfig)
	if err != nil {
		return nil, nil, errors.WithMessage(err, "failed to create transactor")
	}

	peerFilter := func(peer fab.Peer) bool {
		if !cc.greylist.Accept(peer) {
			return false
		}
		if o.TargetFilter != nil && !o.TargetFilter.Accept(peer) {
			return false
		}
		return true
	}

	clientContext := &invoke.ClientContext{
		Selection:    cc.context.SelectionService(),
		Discovery:    cc.context.DiscoveryService(),
		Membership:   cc.membership,
		Transactor:   transactor,
		EventService: cc.eventService,
	}

	requestContext := &invoke.RequestContext{
		Request:         invoke.Request(request),
		Opts:            invoke.Opts(o),
		Response:        invoke.Response{},
		RetryHandler:    retry.New(o.Retry),
		Ctx:             reqCtx,
		SelectionFilter: peerFilter,
	}

	return requestContext, clientContext, nil
}

//prepareOptsFromOptions Reads apitxn.Opts from Option array
func (cc *Client) prepareOptsFromOptions(ctx context.Client, options ...RequestOption) (requestOptions, error) {
	txnOpts := requestOptions{}
	for _, option := range options {
		err := option(ctx, &txnOpts)
		if err != nil {
			return txnOpts, errors.WithMessage(err, "Failed to read opts")
		}
	}
	return txnOpts, nil
}

//addDefaultTimeout adds given default timeout if it is missing in options
func (cc *Client) addDefaultTimeout(ctx context.Client, timeOutType fab.TimeoutType, options ...RequestOption) ([]RequestOption, error) {
	txnOpts := requestOptions{}
	for _, option := range options {
		err := option(ctx, &txnOpts)
		if err != nil {
			return nil, errors.WithMessage(err, "option failed")
		}
	}

	if txnOpts.Timeouts[timeOutType] == 0 {
		//InvokeHandler relies on Execute timeout
		return append(options, WithTimeout(fab.Execute, cc.context.EndpointConfig().TimeoutOrDefault(timeOutType))), nil
	}
	return options, nil
}

// RegisterChaincodeEvent registers chain code event
// @param {chan bool} channel which receives event details when the event is complete
// @returns {object} object handle that should be used to unregister
func (cc *Client) RegisterChaincodeEvent(chainCodeID string, eventFilter string) (fab.Registration, <-chan *fab.CCEvent, error) {
	// Register callback for CE
	return cc.eventService.RegisterChaincodeEvent(chainCodeID, eventFilter)
}

// UnregisterChaincodeEvent removes chain code event registration
func (cc *Client) UnregisterChaincodeEvent(registration fab.Registration) {
	cc.eventService.Unregister(registration)
}
