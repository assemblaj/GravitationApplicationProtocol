package main

import (
	"bufio"
	"context"
	"log"
	"reflect"
	"sort"

	p2p "github.com/assemblaj/GravitationProtocol/pb"

	uuid "github.com/google/uuid"
	host "github.com/libp2p/go-libp2p-host"
	inet "github.com/libp2p/go-libp2p-net"
	protobufCodec "github.com/multiformats/go-multicodec/protobuf"
)

// pattern: /protocol-name/request-or-response-message/version
const gravitationRequest = "/gravitation/gravitationreq/0.0.1"
const gravitationResponse = "/gravitation/gravitationresp/0.0.1"

// take in p2p.GravitationRequest, return true/false
type gravitateReq func(profile []string, orbit []Body, data p2p.GravitationRequest) bool
type gravitateRes func(profile []string, orbit []Body, data p2p.GravitationResponse) bool

type Body struct {
	peerID  string
	profile []string
}

// GravitationProtocol type
type GravitationProtocol struct {
	node        *Node                              // local host
	requests    map[string]*p2p.GravitationRequest // used to access request data from response handlers
	done        chan bool                          // only for demo purposes to stop main from terminating
	profile     []string
	orbit       []Body
	reqCallback gravitateReq
	resCallback gravitateRes
}

func gravitateIfEqualReq(profile []string, orbit []Body, data p2p.GravitationRequest) bool {
	remoteProfile := make([]string, len(profile))
	copy(remoteProfile, data.Profile)

	sort.Strings(profile)
	sort.Strings(remoteProfile)
	return reflect.DeepEqual(profile, remoteProfile)
}

func gravitateIfEqualRes(profile []string, orbit []Body, data p2p.GravitationResponse) bool {
	remoteProfile := make([]string, len(profile))
	copy(remoteProfile, data.Profile)

	sort.Strings(profile)
	sort.Strings(remoteProfile)
	return reflect.DeepEqual(profile, remoteProfile)
}

// Create instance of protocol
func NewGravitationProtocol(node *Node, done chan bool, profile []string, orbit []Body) *GravitationProtocol {
	p := &GravitationProtocol{
		node:        node,
		requests:    make(map[string]*p2p.GravitationRequest),
		done:        done,
		orbit:       orbit,
		profile:     profile,
		reqCallback: gravitateIfEqualReq,
		resCallback: gravitateIfEqualRes}

	node.SetStreamHandler(gravitationRequest, p.onGravitationRequest)
	node.SetStreamHandler(gravitationResponse, p.onGravitationResponse)
	return p
}

// remote peer requests handler
func (p *GravitationProtocol) onGravitationRequest(s inet.Stream) {

	// get request data
	data := &p2p.GravitationRequest{}
	decoder := protobufCodec.Multicodec(nil).Decoder(bufio.NewReader(s))
	err := decoder.Decode(data)
	if err != nil {
		log.Println(err)
		return
	}

	log.Printf("%s: Received gravitation request from %s. Profile: %s SubOrbit: %s.", s.Conn().LocalPeer(), s.Conn().RemotePeer(), data.Profile, data.SubOrbit)

	valid := p.node.authenticateMessage(data, data.MessageData)

	if !valid {
		log.Println("Failed to authenticate message")
		return
	}

	if p.reqCallback(p.profile, p.orbit, *data) {
		p.orbit = append(p.orbit, Body{peerID: s.Conn().RemotePeer().String(), profile: data.Profile})
	}

	// generate response message

	suborbit := []*p2p.GravitationResponse_SubOrbit{}
	for _, body := range p.orbit {
		suborbit = append(suborbit, &(p2p.GravitationResponse_SubOrbit{
			PeerId:  body.peerID,
			Profile: body.profile}))
	}

	resp := &p2p.GravitationResponse{MessageData: p.node.NewMessageData(data.MessageData.Id, false),
		Profile:  p.profile,
		SubOrbit: suborbit}

	log.Printf("%s: Sending gravitation response to %s. Message id: %s Profile: %s SubOrbit: %s....", s.Conn().LocalPeer(), s.Conn().RemotePeer(), data.MessageData.Id, resp.Profile, resp.SubOrbit)

	// sign the data
	signature, err := p.node.signProtoMessage(resp)
	if err != nil {
		log.Println("failed to sign response")
		return
	}

	// add the signature to the message
	resp.MessageData.Sign = signature

	// send the response
	s, respErr := p.node.NewStream(context.Background(), s.Conn().RemotePeer(), gravitationResponse)
	if respErr != nil {
		log.Println(respErr)
		return
	}

	ok := p.node.sendProtoMessage(resp, s)

	if ok {
		log.Printf("%s: Gravitation response to %s sent.", s.Conn().LocalPeer().String(), s.Conn().RemotePeer().String())
	}
}

// remote gravitation response handler
func (p *GravitationProtocol) onGravitationResponse(s inet.Stream) {
	data := &p2p.GravitationResponse{}
	decoder := protobufCodec.Multicodec(nil).Decoder(bufio.NewReader(s))
	err := decoder.Decode(data)
	if err != nil {
		return
	}

	valid := p.node.authenticateMessage(data, data.MessageData)

	if !valid {
		log.Println("Failed to authenticate message")
		return
	}

	if p.resCallback(p.profile, p.orbit, *data) {
		p.orbit = append(p.orbit, Body{peerID: s.Conn().RemotePeer().String(), profile: data.Profile})
	}

	// locate request data and remove it if found
	_, ok := p.requests[data.MessageData.Id]
	if ok {
		// remove request from map as we have processed it here
		delete(p.requests, data.MessageData.Id)
	} else {
		log.Println("Failed to locate request data boject for response")
		return
	}

	log.Printf("%s: Received gravitation response from %s. Message id:%s. Profile: %s SubOrbit: %s.", s.Conn().LocalPeer(), s.Conn().RemotePeer(), data.MessageData.Id, data.Profile, data.SubOrbit)
	p.done <- true
}

// Gravitation funciton
// Performs gravitation
// Takes:
// host host.Host:
// profile []string:  Array that represents the host's properties
// orbit []Body:   Array that represents all the [planetary] 'bodies' in your orbit
// reqCallback gravitateReq:  Validation rules for request (== by default)
// resCallback gravitateRes:  Validaiton rules for response (== by default)
func (p *GravitationProtocol) Gravitation(host host.Host) bool {

	log.Printf("%s: Sending gravitation to: %s....", p.node.ID(), host.ID())

	suborbit := []*p2p.GravitationRequest_SubOrbit{}
	for _, body := range p.orbit {
		suborbit = append(suborbit, &(p2p.GravitationRequest_SubOrbit{
			PeerId:  body.peerID,
			Profile: body.profile}))
	}

	// create message data
	req := &p2p.GravitationRequest{
		MessageData: p.node.NewMessageData(uuid.New().String(), false),
		Profile:     p.profile,
		SubOrbit:    suborbit}

	// sign the data
	signature, err := p.node.signProtoMessage(req)
	if err != nil {
		log.Println("failed to sign pb data")
		return false
	}

	// add the signature to the message
	req.MessageData.Sign = signature

	s, err := p.node.NewStream(context.Background(), host.ID(), gravitationRequest)
	if err != nil {
		log.Println(err)
		return false
	}

	ok := p.node.sendProtoMessage(req, s)

	if !ok {
		return false
	}

	// store ref request so response handler has access to it
	p.requests[req.MessageData.Id] = req
	log.Printf("%s: Gravitation to: %s was sent. Message Id: %s, Profile: %s SubOrbit: %s", p.node.ID(), host.ID(), req.MessageData.Id, req.Profile, req.SubOrbit)
	return true
}
