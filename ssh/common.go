// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssh

import (
	"crypto"
	"errors"
	"fmt"
	"math/big"
	"sync"

	_ "crypto/sha1"
	_ "crypto/sha256"
	_ "crypto/sha512"
)

// These are string constants in the SSH protocol.
const (
	kexAlgoDH1SHA1  = "diffie-hellman-group1-sha1"
	kexAlgoDH14SHA1 = "diffie-hellman-group14-sha1"
	kexAlgoECDH256  = "ecdh-sha2-nistp256"
	kexAlgoECDH384  = "ecdh-sha2-nistp384"
	kexAlgoECDH521  = "ecdh-sha2-nistp521"
	hostAlgoRSA     = "ssh-rsa"
	hostAlgoDSA     = "ssh-dss"
	compressionNone = "none"
	serviceUserAuth = "ssh-userauth"
	serviceSSH      = "ssh-connection"
)

var supportedKexAlgos = []string{
	kexAlgoECDH256, kexAlgoECDH384, kexAlgoECDH521,
	kexAlgoDH14SHA1, kexAlgoDH1SHA1,
}

var supportedHostKeyAlgos = []string{hostAlgoRSA}
var supportedCompressions = []string{compressionNone}

// hashFuncs keeps the mapping of supported algorithms to their respective
// hashes needed for signature verification.
var hashFuncs = map[string]crypto.Hash{
	KeyAlgoRSA:          crypto.SHA1,
	KeyAlgoDSA:          crypto.SHA1,
	KeyAlgoECDSA256:     crypto.SHA256,
	KeyAlgoECDSA384:     crypto.SHA384,
	KeyAlgoECDSA521:     crypto.SHA512,
	CertAlgoRSAv01:      crypto.SHA1,
	CertAlgoDSAv01:      crypto.SHA1,
	CertAlgoECDSA256v01: crypto.SHA256,
	CertAlgoECDSA384v01: crypto.SHA384,
	CertAlgoECDSA521v01: crypto.SHA512,
}

// dhGroup is a multiplicative group suitable for implementing Diffie-Hellman key agreement.
type dhGroup struct {
	g, p *big.Int
}

func (group *dhGroup) diffieHellman(theirPublic, myPrivate *big.Int) (*big.Int, error) {
	if theirPublic.Sign() <= 0 || theirPublic.Cmp(group.p) >= 0 {
		return nil, errors.New("ssh: DH parameter out of bounds")
	}
	return new(big.Int).Exp(theirPublic, myPrivate, group.p), nil
}

// dhGroup1 is the group called diffie-hellman-group1-sha1 in RFC 4253 and
// Oakley Group 2 in RFC 2409.
var dhGroup1 *dhGroup

var dhGroup1Once sync.Once

func initDHGroup1() {
	p, _ := new(big.Int).SetString("FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7EDEE386BFB5A899FA5AE9F24117C4B1FE649286651ECE65381FFFFFFFFFFFFFFFF", 16)

	dhGroup1 = &dhGroup{
		g: new(big.Int).SetInt64(2),
		p: p,
	}
}

// dhGroup14 is the group called diffie-hellman-group14-sha1 in RFC 4253 and
// Oakley Group 14 in RFC 3526.
var dhGroup14 *dhGroup

var dhGroup14Once sync.Once

func initDHGroup14() {
	p, _ := new(big.Int).SetString("FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7EDEE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3DC2007CB8A163BF0598DA48361C55D39A69163FA8FD24CF5F83655D23DCA3AD961C62F356208552BB9ED529077096966D670C354E4ABC9804F1746C08CA18217C32905E462E36CE3BE39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF6955817183995497CEA956AE515D2261898FA051015728E5A8AACAA68FFFFFFFFFFFFFFFF", 16)

	dhGroup14 = &dhGroup{
		g: new(big.Int).SetInt64(2),
		p: p,
	}
}

// UnexpectedMessageError results when the SSH message that we received didn't
// match what we wanted.
type UnexpectedMessageError struct {
	expected, got uint8
}

func (u UnexpectedMessageError) Error() string {
	return fmt.Sprintf("ssh: unexpected message type %d (expected %d)", u.got, u.expected)
}

// ParseError results from a malformed SSH message.
type ParseError struct {
	msgType uint8
}

func (p ParseError) Error() string {
	return fmt.Sprintf("ssh: parse error in message type %d", p.msgType)
}

type handshakeMagics struct {
	clientVersion, serverVersion []byte
	clientKexInit, serverKexInit []byte
}

func findCommonAlgorithm(clientAlgos []string, serverAlgos []string) (commonAlgo string, ok bool) {
	for _, clientAlgo := range clientAlgos {
		for _, serverAlgo := range serverAlgos {
			if clientAlgo == serverAlgo {
				return clientAlgo, true
			}
		}
	}
	return
}

func findCommonCipher(clientCiphers []string, serverCiphers []string) (commonCipher string, ok bool) {
	for _, clientCipher := range clientCiphers {
		for _, serverCipher := range serverCiphers {
			// reject the cipher if we have no cipherModes definition
			if clientCipher == serverCipher && cipherModes[clientCipher] != nil {
				return clientCipher, true
			}
		}
	}
	return
}

func findAgreedAlgorithms(transport *transport, clientKexInit, serverKexInit *kexInitMsg) (kexAlgo, hostKeyAlgo string, ok bool) {
	kexAlgo, ok = findCommonAlgorithm(clientKexInit.KexAlgos, serverKexInit.KexAlgos)
	if !ok {
		return
	}

	hostKeyAlgo, ok = findCommonAlgorithm(clientKexInit.ServerHostKeyAlgos, serverKexInit.ServerHostKeyAlgos)
	if !ok {
		return
	}

	transport.writer.cipherAlgo, ok = findCommonCipher(clientKexInit.CiphersClientServer, serverKexInit.CiphersClientServer)
	if !ok {
		return
	}

	transport.reader.cipherAlgo, ok = findCommonCipher(clientKexInit.CiphersServerClient, serverKexInit.CiphersServerClient)
	if !ok {
		return
	}

	transport.writer.macAlgo, ok = findCommonAlgorithm(clientKexInit.MACsClientServer, serverKexInit.MACsClientServer)
	if !ok {
		return
	}

	transport.reader.macAlgo, ok = findCommonAlgorithm(clientKexInit.MACsServerClient, serverKexInit.MACsServerClient)
	if !ok {
		return
	}

	transport.writer.compressionAlgo, ok = findCommonAlgorithm(clientKexInit.CompressionClientServer, serverKexInit.CompressionClientServer)
	if !ok {
		return
	}

	transport.reader.compressionAlgo, ok = findCommonAlgorithm(clientKexInit.CompressionServerClient, serverKexInit.CompressionServerClient)
	if !ok {
		return
	}

	ok = true
	return
}

// Cryptographic configuration common to both ServerConfig and ClientConfig.
type CryptoConfig struct {
	// The allowed key exchanges algorithms. If unspecified then a
	// default set of algorithms is used.
	KeyExchanges []string

	// The allowed cipher algorithms. If unspecified then DefaultCipherOrder is
	// used.
	Ciphers []string

	// The allowed MAC algorithms. If unspecified then DefaultMACOrder is used.
	MACs []string
}

func (c *CryptoConfig) ciphers() []string {
	if c.Ciphers == nil {
		return DefaultCipherOrder
	}
	return c.Ciphers
}

func (c *CryptoConfig) kexes() []string {
	if c.KeyExchanges == nil {
		return defaultKeyExchangeOrder
	}
	return c.KeyExchanges
}

func (c *CryptoConfig) macs() []string {
	if c.MACs == nil {
		return DefaultMACOrder
	}
	return c.MACs
}

// serialize a signed slice according to RFC 4254 6.6. The name should
// be a key type name, rather than a cert type name.
func serializeSignature(name string, sig []byte) []byte {
	length := stringLength(len(name))
	length += stringLength(len(sig))

	ret := make([]byte, length)
	r := marshalString(ret, []byte(name))
	r = marshalString(r, sig)

	return ret
}

// MarshalPublicKey serializes a supported key or certificate for use
// by the SSH wire protocol. It can be used for comparison with the
// pubkey argument of ServerConfig's PublicKeyCallback as well as for
// generating an authorized_keys or host_keys file.
func MarshalPublicKey(key PublicKey) []byte {
	// See also RFC 4253 6.6.
	algoname := key.PrivateKeyAlgo()
	blob := key.Marshal()

	length := stringLength(len(algoname))
	length += len(blob)
	ret := make([]byte, length)
	r := marshalString(ret, []byte(algoname))
	copy(r, blob)
	return ret
}

// pubAlgoToPrivAlgo returns the private key algorithm format name that
// corresponds to a given public key algorithm format name.  For most
// public keys, the private key algorithm name is the same.  For some
// situations, such as openssh certificates, the private key algorithm and
// public key algorithm names differ.  This accounts for those situations.
func pubAlgoToPrivAlgo(pubAlgo string) string {
	switch pubAlgo {
	case CertAlgoRSAv01:
		return KeyAlgoRSA
	case CertAlgoDSAv01:
		return KeyAlgoDSA
	case CertAlgoECDSA256v01:
		return KeyAlgoECDSA256
	case CertAlgoECDSA384v01:
		return KeyAlgoECDSA384
	case CertAlgoECDSA521v01:
		return KeyAlgoECDSA521
	}
	return pubAlgo
}

// buildDataSignedForAuth returns the data that is signed in order to prove
// posession of a private key. See RFC 4252, section 7.
func buildDataSignedForAuth(sessionId []byte, req userAuthRequestMsg, algo, pubKey []byte) []byte {
	user := []byte(req.User)
	service := []byte(req.Service)
	method := []byte(req.Method)

	length := stringLength(len(sessionId))
	length += 1
	length += stringLength(len(user))
	length += stringLength(len(service))
	length += stringLength(len(method))
	length += 1
	length += stringLength(len(algo))
	length += stringLength(len(pubKey))

	ret := make([]byte, length)
	r := marshalString(ret, sessionId)
	r[0] = msgUserAuthRequest
	r = r[1:]
	r = marshalString(r, user)
	r = marshalString(r, service)
	r = marshalString(r, method)
	r[0] = 1
	r = r[1:]
	r = marshalString(r, algo)
	r = marshalString(r, pubKey)
	return ret
}

// safeString sanitises s according to RFC 4251, section 9.2.
// All control characters except tab, carriage return and newline are
// replaced by 0x20.
func safeString(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c < 0x20 && c != 0xd && c != 0xa && c != 0x9 {
			out[i] = 0x20
		}
	}
	return string(out)
}

func appendU16(buf []byte, n uint16) []byte {
	return append(buf, byte(n>>8), byte(n))
}

func appendU32(buf []byte, n uint32) []byte {
	return append(buf, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

func appendInt(buf []byte, n int) []byte {
	return appendU32(buf, uint32(n))
}

func appendString(buf []byte, s string) []byte {
	buf = appendU32(buf, uint32(len(s)))
	buf = append(buf, s...)
	return buf
}

func appendBool(buf []byte, b bool) []byte {
	if b {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	return buf
}

// newCond is a helper to hide the fact that there is no usable zero
// value for sync.Cond.
func newCond() *sync.Cond { return sync.NewCond(new(sync.Mutex)) }

// window represents the buffer available to clients
// wishing to write to a channel.
type window struct {
	*sync.Cond
	win uint32 // RFC 4254 5.2 says the window size can grow to 2^32-1
}

// add adds win to the amount of window available
// for consumers.
func (w *window) add(win uint32) bool {
	// a zero sized window adjust is a noop.
	if win == 0 {
		return true
	}
	w.L.Lock()
	if w.win+win < win {
		w.L.Unlock()
		return false
	}
	w.win += win
	// It is unusual that multiple goroutines would be attempting to reserve
	// window space, but not guaranteed. Use broadcast to notify all waiters
	// that additional window is available.
	w.Broadcast()
	w.L.Unlock()
	return true
}

// reserve reserves win from the available window capacity.
// If no capacity remains, reserve will block. reserve may
// return less than requested.
func (w *window) reserve(win uint32) uint32 {
	w.L.Lock()
	for w.win == 0 {
		w.Wait()
	}
	if w.win < win {
		win = w.win
	}
	w.win -= win
	w.L.Unlock()
	return win
}
