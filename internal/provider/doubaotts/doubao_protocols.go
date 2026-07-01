package doubaotts

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/gorilla/websocket"
)

var (
	ErrWriteBuffer      = errors.New("write buffer error")
	ErrReadBuffer       = errors.New("read buffer error")
	ErrSendWebsocketMsg = errors.New("send websocket message error")
	ErrEncodeJSON       = errors.New("encode json error")
)

type (
	// EventType defines the event type which determines the event of the message.
	EventType int32
	// MsgType defines message type which determines how the message will be
	// serialized with the protocol.
	MsgType uint8
	// MsgTypeFlagBits defines the 4-bit message-type specific flags. The specific
	// values should be defined in each specific usage scenario.
	MsgTypeFlagBits uint8
	// VersionBits defines the 4-bit version type.
	VersionBits uint8
	// HeaderSizeBits defines the 4-bit header-size type.
	HeaderSizeBits uint8
	// SerializationBits defines the 4-bit serialization method type.
	SerializationBits uint8
	// CompressionBits defines the 4-bit compression method type.
	CompressionBits uint8
)

const (
	MsgTypeFlagNoSeq       MsgTypeFlagBits = 0     // Non-terminal packet with no sequence
	MsgTypeFlagPositiveSeq MsgTypeFlagBits = 0b1   // Non-terminal packet with sequence > 0
	MsgTypeFlagLastNoSeq   MsgTypeFlagBits = 0b10  // last packet with no sequence
	MsgTypeFlagNegativeSeq MsgTypeFlagBits = 0b11  // last packet with sequence < 0
	MsgTypeFlagWithEvent   MsgTypeFlagBits = 0b100 // Payload contains event number (int32)
)

const (
	Version1 VersionBits = iota + 1
	Version2
	Version3
	Version4
)

const (
	HeaderSize4 HeaderSizeBits = iota + 1
	HeaderSize8
	HeaderSize12
	HeaderSize16
)

const (
	SerializationRaw    SerializationBits = 0
	SerializationJSON   SerializationBits = 0b1
	SerializationThrift SerializationBits = 0b11
	SerializationCustom SerializationBits = 0b1111
)

const (
	CompressionNone   CompressionBits = 0
	CompressionGzip   CompressionBits = 0b1
	CompressionCustom CompressionBits = 0b1111
)

const (
	MsgTypeInvalid              MsgType = 0
	MsgTypeFullClientRequest    MsgType = 0b1
	MsgTypeAudioOnlyClient      MsgType = 0b10
	MsgTypeFullServerResponse   MsgType = 0b1001
	MsgTypeAudioOnlyServer      MsgType = 0b1011
	MsgTypeFrontEndResultServer MsgType = 0b1100
	MsgTypeError                MsgType = 0b1111

	MsgTypeServerACK = MsgTypeAudioOnlyServer
)

func (t MsgType) String() string {
	switch t {
	case MsgTypeFullClientRequest:
		return "MsgType_FullClientRequest"
	case MsgTypeAudioOnlyClient:
		return "MsgType_AudioOnlyClient"
	case MsgTypeFullServerResponse:
		return "MsgType_FullServerResponse"
	case MsgTypeAudioOnlyServer:
		return "MsgType_AudioOnlyServer" // MsgTypeServerACK
	case MsgTypeError:
		return "MsgType_Error"
	case MsgTypeFrontEndResultServer:
		return "MsgType_FrontEndResultServer"
	default:
		return fmt.Sprintf("MsgType_(%d)", t)
	}
}

const (
	// EventtypeNone Default event, applicable for scenarios not using events or not requiring event transmission,
	// or for scenarios using events, non-zero values can be used to validate event legitimacy
	EventtypeNone EventType = 0
	// EventtypeStartconnection 1 ~ 49 for upstream Connection events
	EventtypeStartconnection  EventType = 1
	EventTypeStartTask        EventType = 1 // Alias of "StartConnection"
	EventtypeFinishconnection EventType = 2
	EventTypeFinishTask       EventType = 2 // Alias of "FinishConnection"
	// 50 ~ 99 for downstream Connection events
	// Connection established successfully
	EventTypeConnectionStarted EventType = 50
	EventTypeTaskStarted       EventType = 50 // Alias of "ConnectionStarted"
	// Connection failed (possibly due to authentication failure)
	EventTypeConnectionFailed EventType = 51
	EventTypeTaskFailed       EventType = 51 // Alias of "ConnectionFailed"
	// Connection ended
	EventTypeConnectionFinished EventType = 52
	EventTypeTaskFinished       EventType = 52 // Alias of "ConnectionFinished"
	// 100 ~ 149 for upstream Session events
	EventTypeStartSession  EventType = 100
	EventTypeCancelSession EventType = 101
	EventTypeFinishSession EventType = 102
	// 150 ~ 199 for downstream Session events
	EventTypeSessionStarted  EventType = 150
	EventTypeSessionCanceled EventType = 151
	EventTypeSessionFinished EventType = 152
	EventTypeSessionFailed   EventType = 153
	// Usage events
	EventTypeUsageResponse EventType = 154
	EventTypeChargeData    EventType = 154 // Alias of "UsageResponse"
	// 200 ~ 249 for upstream general events
	EventTypeTaskRequest  EventType = 200
	EventTypeUpdateConfig EventType = 201
	// 250 ~ 299 for downstream general events
	EventTypeAudioMuted EventType = 250
	// 300 ~ 349 for upstream TTS events
	EventTypeSayHello EventType = 300
	// 350 ~ 399 for downstream TTS events
	EventTypeTTSSentenceStart     EventType = 350
	EventTypeTTSSentenceEnd       EventType = 351
	EventTypeTTSResponse          EventType = 352
	EventTypeTTSEnded             EventType = 359
	EventTypePodcastRoundStart    EventType = 360
	EventTypePodcastRoundResponse EventType = 361
	EventTypePodcastRoundEnd      EventType = 362
	// 450 ~ 499 for downstream ASR events
	EventTypeASRInfo     EventType = 450
	EventTypeASRResponse EventType = 451
	EventTypeASREnded    EventType = 459
	// 500 ~ 549 for upstream dialogue events
	// (Ground-Truth-Alignment) text for speech synthesis
	EventTypeChatTTSText EventType = 500
	// 550 ~ 599 for downstream dialogue events
	EventTypeChatResponse EventType = 550
	EventTypeChatEnded    EventType = 559
	// 650 ~ 699 for downstream dialogue events
	// Events for source (original) language subtitle.
	EventTypeSourceSubtitleStart    EventType = 650
	EventTypeSourceSubtitleResponse EventType = 651
	EventTypeSourceSubtitleEnd      EventType = 652
	// Events for target (translation) language subtitle.
	EventTypeTranslationSubtitleStart    EventType = 653
	EventTypeTranslationSubtitleResponse EventType = 654
	EventTypeTranslationSubtitleEnd      EventType = 655
)

func (t EventType) String() string {
	switch t {
	case EventtypeNone:
		return "EventType_None"
	case EventtypeStartconnection:
		return "EventType_StartConnection"
	case EventtypeFinishconnection:
		return "EventType_FinishConnection"
	case EventTypeConnectionStarted:
		return "EventTypeConnectionStarted"
	case EventTypeConnectionFailed:
		return "EventTypeConnectionFailed"
	case EventTypeConnectionFinished:
		return "EventTypeConnectionFinished"
	case EventTypeStartSession:
		return "EventTypeStartSession"
	case EventTypeCancelSession:
		return "EventTypeCancelSession"
	case EventTypeFinishSession:
		return "EventTypeFinishSession"
	case EventTypeSessionStarted:
		return "EventTypeSessionStarted"
	case EventTypeSessionCanceled:
		return "EventTypeSessionCanceled"
	case EventTypeSessionFinished:
		return "EventTypeSessionFinished"
	case EventTypeSessionFailed:
		return "EventTypeSessionFailed"
	case EventTypeUsageResponse:
		return "EventTypeUsageResponse"
	case EventTypeTaskRequest:
		return "EventTypeTaskRequest"
	case EventTypeUpdateConfig:
		return "EventTypeUpdateConfig"
	case EventTypeAudioMuted:
		return "EventTypeAudioMuted"
	case EventTypeSayHello:
		return "EventTypeSayHello"
	case EventTypeTTSSentenceStart:
		return "EventTypeTTSSentenceStart"
	case EventTypeTTSSentenceEnd:
		return "EventTypeTTSSentenceEnd"
	case EventTypeTTSResponse:
		return "EventTypeTTSResponse"
	case EventTypeTTSEnded:
		return "EventTypeTTSEnded"
	case EventTypePodcastRoundStart:
		return "EventTypePodcastRoundStart"
	case EventTypePodcastRoundResponse:
		return "EventTypePodcastRoundResponse"
	case EventTypePodcastRoundEnd:
		return "EventTypePodcastRoundEnd"
	case EventTypeASRInfo:
		return "EventTypeASRInfo"
	case EventTypeASRResponse:
		return "EventTypeASRResponse"
	case EventTypeASREnded:
		return "EventTypeASREnded"
	case EventTypeChatTTSText:
		return "EventTypeChatTTSText"
	case EventTypeChatResponse:
		return "EventTypeChatResponse"
	case EventTypeChatEnded:
		return "EventTypeChatEnded"
	case EventTypeSourceSubtitleStart:
		return "EventTypeSourceSubtitleStart"
	case EventTypeSourceSubtitleResponse:
		return "EventTypeSourceSubtitleResponse"
	case EventTypeSourceSubtitleEnd:
		return "EventTypeSourceSubtitleEnd"
	case EventTypeTranslationSubtitleStart:
		return "EventTypeTranslationSubtitleStart"
	case EventTypeTranslationSubtitleResponse:
		return "EventTypeTranslationSubtitleResponse"
	case EventTypeTranslationSubtitleEnd:
		return "EventTypeTranslationSubtitleEnd"
	default:
		return fmt.Sprintf("EventType_(%d)", t)
	}
}

// 0                 1                 2                 3
// | 0 1 2 3 4 5 6 7 | 0 1 2 3 4 5 6 7 | 0 1 2 3 4 5 6 7 | 0 1 2 3 4 5 6 7 |
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
// |    Version      |   Header Size   |     Msg Type    |      Flags      |
// |   (4 bits)      |    (4 bits)     |     (4 bits)    |     (4 bits)    |
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
// | Serialization   |   Compression   |           Reserved                |
// |   (4 bits)      |    (4 bits)     |           (8 bits)                |
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
// |                                                                       |
// |                   Optional Header Extensions                          |
// |                     (if Header Size > 1)                              |
// |                                                                       |
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
// |                                                                       |
// |                           Payload                                     |
// |                      (variable length)                                |
// |                                                                       |
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+

type Message struct {
	SessionID     string
	ConnectID     string
	Payload       []byte
	EventType     EventType
	Sequence      int32
	ErrorCode     uint32
	Version       VersionBits
	HeaderSize    HeaderSizeBits
	MsgType       MsgType
	MsgTypeFlag   MsgTypeFlagBits
	Serialization SerializationBits
	Compression   CompressionBits
}

func NewMessageFromBytes(data []byte) (*Message, error) {
	if len(data) < 3 {
		return nil, fmt.Errorf("data too short: expected at least 3 bytes, got %d", len(data))
	}

	typeAndFlag := data[1]

	msg, err := NewMessage(MsgType(typeAndFlag>>4), MsgTypeFlagBits(typeAndFlag&0b00001111))
	if err != nil {
		return nil, err
	}

	if err := msg.Unmarshal(data); err != nil {
		return nil, err
	}

	return msg, nil
}

func NewMessage(msgType MsgType, flag MsgTypeFlagBits) (*Message, error) {
	return &Message{
		MsgType:       msgType,
		MsgTypeFlag:   flag,
		Version:       Version1,
		HeaderSize:    HeaderSize4,
		Serialization: SerializationJSON,
		Compression:   CompressionNone,
	}, nil
}

func (m *Message) String() string {
	switch m.MsgType {
	case MsgTypeAudioOnlyServer, MsgTypeAudioOnlyClient:
		if m.MsgTypeFlag == MsgTypeFlagPositiveSeq || m.MsgTypeFlag == MsgTypeFlagNegativeSeq {
			return fmt.Sprintf("%s, %s, Sequence: %d, PayloadSize: %d", m.MsgType, m.EventType, m.Sequence, len(m.Payload))
		}
		return fmt.Sprintf("%s, %s, PayloadSize: %d", m.MsgType, m.EventType, len(m.Payload))
	case MsgTypeError:
		return fmt.Sprintf("%s, %s, ErrorCode: %d, Payload: %s", m.MsgType, m.EventType, m.ErrorCode, string(m.Payload))
	default:
		if m.MsgTypeFlag == MsgTypeFlagPositiveSeq || m.MsgTypeFlag == MsgTypeFlagNegativeSeq {
			return fmt.Sprintf("%s, %s, Sequence: %d, Payload: %s",
				m.MsgType, m.EventType, m.Sequence, string(m.Payload))
		}
		return fmt.Sprintf("%s, %s, Payload: %s", m.MsgType, m.EventType, string(m.Payload))
	}
}

func (m *Message) Marshal() ([]byte, error) {
	buf := new(bytes.Buffer)

	header := []uint8{
		uint8(m.Version)<<4 | uint8(m.HeaderSize),
		uint8(m.MsgType)<<4 | uint8(m.MsgTypeFlag),
		uint8(m.Serialization)<<4 | uint8(m.Compression),
	}

	headerSize := 4 * int(m.HeaderSize)
	if padding := headerSize - len(header); padding > 0 {
		header = append(header, make([]uint8, padding)...)
	}

	if err := binary.Write(buf, binary.BigEndian, header); err != nil {
		return nil, errors.Join(err, ErrEncodeJSON)
	}

	writers, err := m.writers()
	if err != nil {
		return nil, err
	}

	for _, write := range writers {
		if err := write(buf); err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}

func (m *Message) Unmarshal(data []byte) error {
	buf := bytes.NewBuffer(data)

	versionAndHeaderSize, err := buf.ReadByte()
	if err != nil {
		return errors.Join(err, ErrWriteBuffer)
	}

	m.Version = VersionBits(versionAndHeaderSize >> 4)
	m.HeaderSize = HeaderSizeBits(versionAndHeaderSize & 0b00001111)

	_, err = buf.ReadByte()
	if err != nil {
		return errors.Join(err, ErrWriteBuffer)
	}

	serializationCompression, err := buf.ReadByte()
	if err != nil {
		return errors.Join(err, ErrReadBuffer)
	}

	m.Serialization = SerializationBits(serializationCompression & 0b11110000)
	m.Compression = CompressionBits(serializationCompression & 0b00001111)

	headerSize := 4 * int(m.HeaderSize)
	readSize := 3
	if paddingSize := headerSize - readSize; paddingSize > 0 {
		if n, err := buf.Read(make([]byte, paddingSize)); err != nil || n < paddingSize {
			return fmt.Errorf("insufficient header bytes: expected %d, got %d", paddingSize, n)
		}
	}

	readers, err := m.readers()
	if err != nil {
		return errors.Join(err, ErrReadBuffer)
	}

	for _, read := range readers {
		if err := read(buf); err != nil {
			return errors.Join(err, ErrReadBuffer)
		}
	}

	if _, err := buf.ReadByte(); err != io.EOF {
		return fmt.Errorf("unexpected data after message: %w", err)
	}

	return nil
}

func (m *Message) writers() (writers []func(*bytes.Buffer) error, _ error) {
	if m.MsgTypeFlag == MsgTypeFlagWithEvent {
		writers = append(writers, m.writeEvent, m.writeSessionID)
		if m.EventType == EventTypeConnectionStarted || m.EventType == EventTypeConnectionFailed || m.EventType == EventTypeConnectionFinished {
			writers = append(writers, m.writeConnectID)
		}
	}

	switch m.MsgType {
	case MsgTypeFullClientRequest, MsgTypeFullServerResponse, MsgTypeFrontEndResultServer, MsgTypeAudioOnlyClient, MsgTypeAudioOnlyServer:
		if m.MsgTypeFlag == MsgTypeFlagPositiveSeq || m.MsgTypeFlag == MsgTypeFlagNegativeSeq {
			writers = append(writers, m.writeSequence)
		}
	case MsgTypeError:
		writers = append(writers, m.writeErrorCode)
	default:
		return nil, fmt.Errorf("unsupported message type: %d", m.MsgType)
	}

	writers = append(writers, m.writePayload)
	return writers, nil
}

func (m *Message) writeEvent(buf *bytes.Buffer) error {
	err := binary.Write(buf, binary.BigEndian, m.EventType)
	if err != nil {
		return errors.Join(err, ErrWriteBuffer)
	}
	return nil
}

func (m *Message) writeSessionID(buf *bytes.Buffer) error {
	switch m.EventType {
	case EventtypeStartconnection, EventtypeFinishconnection,
		EventTypeConnectionStarted, EventTypeConnectionFailed:
		return nil
	}

	size := len(m.SessionID)
	if size > math.MaxUint32 {
		return fmt.Errorf("session ID size (%d) exceeds max(uint32)", size)
	}

	if err := binary.Write(buf, binary.BigEndian, uint32(size)); err != nil {
		return errors.Join(err, ErrWriteBuffer)
	}

	buf.WriteString(m.SessionID)
	return nil
}

func (m *Message) writeConnectID(buf *bytes.Buffer) error {
	size := len(m.ConnectID)
	if size > math.MaxUint32 {
		return fmt.Errorf("connect ID size (%d) exceeds max(uint32)", size)
	}

	if err := binary.Write(buf, binary.BigEndian, uint32(size)); err != nil {
		return errors.Join(err, ErrWriteBuffer)
	}

	buf.WriteString(m.ConnectID)
	return nil
}

func (m *Message) writeSequence(buf *bytes.Buffer) error {
	err := binary.Write(buf, binary.BigEndian, m.Sequence)
	if err != nil {
		return errors.Join(err, ErrWriteBuffer)
	}
	return nil
}

func (m *Message) writeErrorCode(buf *bytes.Buffer) error {
	err := binary.Write(buf, binary.BigEndian, m.ErrorCode)
	if err != nil {
		return errors.Join(err, ErrWriteBuffer)
	}
	return nil
}

func (m *Message) writePayload(buf *bytes.Buffer) error {
	size := len(m.Payload)
	if size > math.MaxUint32 {
		return fmt.Errorf("payload size (%d) exceeds max(uint32)", size)
	}

	if err := binary.Write(buf, binary.BigEndian, uint32(size)); err != nil {
		return errors.Join(err, ErrReadBuffer)
	}

	buf.Write(m.Payload)
	return nil
}

func (m *Message) readers() (readers []func(*bytes.Buffer) error, _ error) {
	switch m.MsgType {
	case MsgTypeFullClientRequest, MsgTypeFullServerResponse, MsgTypeFrontEndResultServer, MsgTypeAudioOnlyClient, MsgTypeAudioOnlyServer:
		if m.MsgTypeFlag == MsgTypeFlagPositiveSeq || m.MsgTypeFlag == MsgTypeFlagNegativeSeq {
			readers = append(readers, m.readSequence)
		}
	case MsgTypeError:
		readers = append(readers, m.readErrorCode)
	default:
		return nil, fmt.Errorf("unsupported message type: %d", m.MsgType)
	}

	if m.MsgTypeFlag == MsgTypeFlagWithEvent {
		readers = append(readers, m.readEvent, m.readSessionID, m.readConnectID)
	}

	readers = append(readers, m.readPayload)
	return readers, nil
}

func (m *Message) readEvent(buf *bytes.Buffer) error {
	err := binary.Read(buf, binary.BigEndian, &m.EventType)
	if err != nil {
		return errors.Join(err, ErrReadBuffer)
	}
	return nil
}

func (m *Message) readSessionID(buf *bytes.Buffer) error {
	switch m.EventType {
	case EventtypeStartconnection, EventtypeFinishconnection,
		EventTypeConnectionStarted, EventTypeConnectionFailed,
		EventTypeConnectionFinished:
		return nil
	}

	var size uint32
	if err := binary.Read(buf, binary.BigEndian, &size); err != nil {
		return errors.Join(err, ErrReadBuffer)
	}

	if size > 0 {
		m.SessionID = string(buf.Next(int(size)))
	}

	return nil
}

func (m *Message) readConnectID(buf *bytes.Buffer) error {
	switch m.EventType {
	case EventTypeConnectionStarted, EventTypeConnectionFailed,
		EventTypeConnectionFinished:
	default:
		return nil
	}

	var size uint32
	if err := binary.Read(buf, binary.BigEndian, &size); err != nil {
		return errors.Join(err, ErrReadBuffer)
	}

	if size > 0 {
		m.ConnectID = string(buf.Next(int(size)))
	}

	return nil
}

func (m *Message) readSequence(buf *bytes.Buffer) error {
	err := binary.Read(buf, binary.BigEndian, &m.Sequence)
	if err != nil {
		return errors.Join(err, ErrReadBuffer)
	}
	return nil
}

func (m *Message) readErrorCode(buf *bytes.Buffer) error {
	err := binary.Read(buf, binary.BigEndian, &m.ErrorCode)
	if err != nil {
		return errors.Join(err, ErrReadBuffer)
	}
	return nil
}

func (m *Message) readPayload(buf *bytes.Buffer) error {
	var size uint32
	if err := binary.Read(buf, binary.BigEndian, &size); err != nil {
		return errors.Join(err, ErrReadBuffer)
	}

	if size > 0 {
		m.Payload = buf.Next(int(size))
	}

	return nil
}

func ReceiveMessage(conn *websocket.Conn) (*Message, error) {
	mt, frame, err := conn.ReadMessage()
	if err != nil {
		return nil, errors.Join(err, ErrReadBuffer)
	}
	if mt != websocket.BinaryMessage && mt != websocket.TextMessage {
		return nil, fmt.Errorf("unexpected Websocket message type: %d", mt)
	}
	msg, err := NewMessageFromBytes(frame)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func WaitForEvent(conn *websocket.Conn, msgType MsgType, eventType EventType) (*Message, error) {
	for {
		msg, err := ReceiveMessage(conn)
		if err != nil {
			return nil, err
		}
		if msg.MsgType != msgType || msg.EventType != eventType {
			return nil, fmt.Errorf("unexpected message: %s", msg)
		}
		if msg.MsgType == msgType && msg.EventType == eventType {
			return msg, nil
		}
	}
}

func FullClientRequest(conn *websocket.Conn, payload []byte) error {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagNoSeq)
	if err != nil {
		return err
	}
	msg.Payload = payload
	frame, err := msg.Marshal()
	if err != nil {
		return err
	}
	err = conn.WriteMessage(websocket.BinaryMessage, frame)
	if err != nil {
		return errors.Join(err, ErrSendWebsocketMsg)
	}
	return nil
}

func AudioOnlyClient(conn *websocket.Conn, payload []byte, flag MsgTypeFlagBits) error {
	msg, err := NewMessage(MsgTypeAudioOnlyClient, flag)
	if err != nil {
		return err
	}
	msg.Payload = payload
	frame, err := msg.Marshal()
	if err != nil {
		return err
	}
	err = conn.WriteMessage(websocket.BinaryMessage, frame)
	if err != nil {
		return errors.Join(err, ErrSendWebsocketMsg)
	}
	return nil
}

func StartConnection(conn *websocket.Conn) error {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		return err
	}
	msg.EventType = EventtypeStartconnection
	msg.Payload = []byte("{}")
	frame, err := msg.Marshal()
	if err != nil {
		return err
	}
	err = conn.WriteMessage(websocket.BinaryMessage, frame)
	if err != nil {
		return errors.Join(err, ErrSendWebsocketMsg)
	}
	return nil
}

func FinishConnection(conn *websocket.Conn) error {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		return err
	}
	msg.EventType = EventtypeFinishconnection
	msg.Payload = []byte("{}")
	frame, err := msg.Marshal()
	if err != nil {
		return err
	}
	err = conn.WriteMessage(websocket.BinaryMessage, frame)
	if err != nil {
		return errors.Join(err, ErrSendWebsocketMsg)
	}
	return nil
}

func StartSession(conn *websocket.Conn, payload []byte, sessionID string) error {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		return err
	}
	msg.EventType = EventTypeStartSession
	msg.SessionID = sessionID
	msg.Payload = payload
	frame, err := msg.Marshal()
	if err != nil {
		return err
	}
	err = conn.WriteMessage(websocket.BinaryMessage, frame)
	if err != nil {
		return errors.Join(err, ErrSendWebsocketMsg)
	}
	return nil
}

func FinishSession(conn *websocket.Conn, sessionID string) error {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		return err
	}
	msg.EventType = EventTypeFinishSession
	msg.SessionID = sessionID
	msg.Payload = []byte("{}")
	frame, err := msg.Marshal()
	if err != nil {
		return err
	}
	err = conn.WriteMessage(websocket.BinaryMessage, frame)
	if err != nil {
		return errors.Join(err, ErrSendWebsocketMsg)
	}
	return nil
}

func CancelSession(conn *websocket.Conn, sessionID string) error {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		return err
	}
	msg.EventType = EventTypeCancelSession
	msg.SessionID = sessionID
	msg.Payload = []byte("{}")
	frame, err := msg.Marshal()
	if err != nil {
		return err
	}
	err = conn.WriteMessage(websocket.BinaryMessage, frame)
	if err != nil {
		return errors.Join(err, ErrSendWebsocketMsg)
	}
	return nil
}

func TaskRequest(conn *websocket.Conn, payload []byte, sessionID string) error {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		return err
	}
	msg.EventType = EventTypeTaskRequest
	msg.SessionID = sessionID
	msg.Payload = payload
	frame, err := msg.Marshal()
	if err != nil {
		return err
	}
	err = conn.WriteMessage(websocket.BinaryMessage, frame)
	if err != nil {
		return errors.Join(err, ErrSendWebsocketMsg)
	}
	return nil
}
