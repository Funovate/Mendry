package password

import "golang.org/x/crypto/bcrypt"

// Bcrypt 使用 cost 12 创建和验证 password hash。
type Bcrypt struct{}

func (Bcrypt) Hash(password []byte) (string, error) {
	hash, err := bcrypt.GenerateFromPassword(password, 12)
	return string(hash), err
}

func (Bcrypt) Compare(hash string, password []byte) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), password)
}
