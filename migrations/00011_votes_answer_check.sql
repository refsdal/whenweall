-- +goose Up
-- Ports answerSchema (main:src/server/polls/schemas.ts:17, z.enum(['yes','ifneedbe','no'])) into
-- the schema itself. validateAnswersTx (internal/polls/participants.go) rejects other values at
-- the service layer; this constraint guarantees no other writer (Claim's UpsertVote, a future code
-- path, a manual fix-up) can ever store one either — votes.answer is echoed verbatim to every
-- viewer (participants[].votes) and read by scoring.
ALTER TABLE votes ADD CONSTRAINT votes_answer_check CHECK (answer IN ('yes', 'ifneedbe', 'no'));

-- +goose Down
ALTER TABLE votes DROP CONSTRAINT votes_answer_check;
