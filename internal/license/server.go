package license

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Server struct {
	cfg         Config
	store       Store
	appAttest   AppAttestValidator
	storeKit    TransactionValidator
	receiptRisk ReceiptRiskAssessor
	mux         *http.ServeMux
}

func NewServer(
	cfg Config,
	store Store,
	appAttest AppAttestValidator,
	storeKit TransactionValidator,
	receiptRisk ReceiptRiskAssessor,
) (*Server, error) {
	if cfg.OfflineGrace == 0 {
		cfg.OfflineGrace = 7 * 24 * time.Hour
	}
	if cfg.MaxAttestations30d == 0 {
		cfg.MaxAttestations30d = 3
	}
	if cfg.AppID == "" {
		return nil, errors.New("app id is required")
	}
	if len(cfg.HMACSecret) < 32 {
		return nil, errors.New("hmac secret must be at least 32 bytes")
	}
	if len(cfg.GrantSigningSecret) < 32 {
		return nil, errors.New("grant signing secret must be at least 32 bytes")
	}
	if store == nil {
		store = NewMemoryStore()
	}
	if appAttest == nil {
		validator, err := NewAppleAppAttestValidator(cfg)
		if err != nil {
			return nil, err
		}
		appAttest = validator
	}
	if storeKit == nil {
		validator, err := NewStoreKitJWSValidator(cfg)
		if err != nil {
			return nil, err
		}
		storeKit = validator
	}
	if receiptRisk == nil {
		receiptRisk = NoopReceiptRiskAssessor{}
	}

	s := &Server{
		cfg:         cfg,
		store:       store,
		appAttest:   appAttest,
		storeKit:    storeKit,
		receiptRisk: receiptRisk,
		mux:         http.NewServeMux(),
	}
	s.mux.HandleFunc("POST /v1/license/challenge", s.handleChallenge)
	s.mux.HandleFunc("POST /v1/license/attest", s.handleAttest)
	s.mux.HandleFunc("POST /v1/license/validate", s.handleValidate)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	var req ChallengeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Purpose != ChallengePurposeAttest && req.Purpose != ChallengePurposeValidate {
		writeError(w, http.StatusBadRequest, "unsupported challenge purpose")
		return
	}
	if strings.TrimSpace(req.InstallID) == "" || strings.TrimSpace(req.KeyID) == "" {
		writeError(w, http.StatusBadRequest, "install_id and key_id are required")
		return
	}
	now := s.cfg.now()
	id, err := randomBase64URL(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	challenge, err := randomBase64URL(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	record := ChallengeRecord{
		ID:          id,
		Purpose:     req.Purpose,
		Challenge:   challenge,
		InstallHash: keyedHash(s.cfg.HMACSecret, req.InstallID),
		KeyHash:     keyedHash(s.cfg.HMACSecret, req.KeyID),
		CreatedAt:   now,
		ExpiresAt:   now.Add(5 * time.Minute),
	}
	if err := s.store.SaveChallenge(record); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ChallengeResponse{
		ChallengeID: id,
		Challenge:   challenge,
		ExpiresAt:   record.ExpiresAt,
	})
}

func (s *Server) handleAttest(w http.ResponseWriter, r *http.Request) {
	var req AttestRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	installHash := keyedHash(s.cfg.HMACSecret, req.InstallID)
	keyHash := keyedHash(s.cfg.HMACSecret, req.KeyID)
	now := s.cfg.now()
	ch, err := s.store.ConsumeChallenge(req.ChallengeID, ChallengePurposeAttest, installHash, keyHash, now)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	attestation, err := base64.StdEncoding.DecodeString(req.AttestationObject)
	if err != nil {
		writeError(w, http.StatusBadRequest, "attestation_object must be base64")
		return
	}
	result, err := s.appAttest.ValidateAttestation(req.KeyID, []byte(ch.Challenge), attestation)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	assessment, err := s.receiptRisk.Assess(result.Receipt)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if assessment.Receipt != nil {
		result.Receipt = assessment.Receipt
	}
	if assessment.Metric != nil && *assessment.Metric > s.cfg.MaxAttestations30d {
		writeError(w, http.StatusForbidden, "app attest receipt risk metric exceeds free access policy")
		return
	}
	count, err := s.store.CountRecentAttestations(installHash, now.AddDate(0, 0, -30))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if count >= s.cfg.MaxAttestations30d {
		writeError(w, http.StatusForbidden, "install has exceeded recent attestation policy")
		return
	}

	trialStart := now
	trialEnd := trialEndDate(trialStart)
	dev := DeviceRecord{
		InstallHash:      installHash,
		KeyHash:          keyHash,
		KeyID:            req.KeyID,
		PublicKeyDER:     result.PublicKeyDER,
		Receipt:          result.Receipt,
		ReceiptMetric:    assessment.Metric,
		AssertionCounter: result.Counter,
		TrialStartDate:   trialStart,
		TrialEndsAt:      trialEnd,
		LastVerifiedAt:   now,
		CreatedAt:        now,
		UpdatedAt:        now,
		AttestationEnv:   result.Environment,
	}
	if err := s.store.SaveDevice(dev); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	snapshot := evaluateGrant(dev, nil, now, s.cfg)
	grant, err := buildSignedGrant(dev, snapshot, now, s.cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, GrantResponse{Grant: grant, Snapshot: snapshot})
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	var req ValidateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	clientData, err := base64.StdEncoding.DecodeString(req.ClientData)
	if err != nil {
		writeError(w, http.StatusBadRequest, "client_data must be base64")
		return
	}
	var data AssertionClientData
	if err := json.Unmarshal(clientData, &data); err != nil {
		writeError(w, http.StatusBadRequest, "client_data must be JSON")
		return
	}
	if data.KeyID != req.KeyID {
		writeError(w, http.StatusBadRequest, "key_id mismatch")
		return
	}
	installHash := keyedHash(s.cfg.HMACSecret, data.InstallID)
	keyHash := keyedHash(s.cfg.HMACSecret, data.KeyID)
	now := s.cfg.now()
	ch, err := s.store.ConsumeChallenge(data.ChallengeID, ChallengePurposeValidate, installHash, keyHash, now)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if data.Challenge != ch.Challenge {
		writeError(w, http.StatusUnauthorized, "challenge mismatch")
		return
	}
	dev, err := s.store.GetDeviceByKeyHash(keyHash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	publicKey, err := x509.ParsePKIXPublicKey(dev.PublicKeyDER)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stored public key is invalid")
		return
	}
	ecdsaKey, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stored public key is not ECDSA")
		return
	}
	assertion, err := base64.StdEncoding.DecodeString(req.Assertion)
	if err != nil {
		writeError(w, http.StatusBadRequest, "assertion must be base64")
		return
	}
	counter, err := s.appAttest.ValidateAssertion(ecdsaKey, dev.AssertionCounter, clientData, assertion)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	transactions := make([]LicenseTransaction, 0, len(req.Transactions))
	for _, jws := range req.Transactions {
		tx, err := s.storeKit.Validate(jws)
		if err != nil {
			writeError(w, http.StatusUnauthorized, fmt.Sprintf("transaction validation failed: %v", err))
			return
		}
		if !isKnownProduct(tx.ProductID) {
			continue
		}
		transactions = append(transactions, tx)
	}
	dev.AssertionCounter = counter
	dev.LastVerifiedAt = now
	dev.UpdatedAt = now
	dev.TransactionIDHashs = transactionHashes(s.cfg.HMACSecret, transactions)
	if err := s.store.SaveDevice(dev); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	snapshot := evaluateGrant(dev, transactions, now, s.cfg)
	grant, err := buildSignedGrant(dev, snapshot, now, s.cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, GrantResponse{Grant: grant, Snapshot: snapshot})
}

func transactionHashes(secret []byte, transactions []LicenseTransaction) []string {
	out := make([]string, 0, len(transactions))
	for _, tx := range transactions {
		if tx.TransactionID != "" {
			out = append(out, keyedHash(secret, tx.TransactionID))
		}
	}
	return out
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "record not found")
	case errors.Is(err, ErrChallengeConsumed):
		writeError(w, http.StatusConflict, "challenge already consumed")
	case errors.Is(err, ErrChallengeExpired):
		writeError(w, http.StatusGone, "challenge expired")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
