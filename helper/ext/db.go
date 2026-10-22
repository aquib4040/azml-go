package ext

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"

	"azml-go/types"
	"azml-go/config"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DatabaseManager struct {
	client    *mongo.Client
	usersCol  *mongo.Collection
	isMongo   bool
	localFile string
	fileLock  sync.Mutex
}

var (
	DB   *DatabaseManager
	once sync.Once
)

func InitDB() *DatabaseManager {
	once.Do(func() {
		cfg := config.Get()
		mgr := &DatabaseManager{
			localFile: "user_data.json",
		}

		if cfg.DatabaseURL != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			clientOpts := options.Client().ApplyURI(cfg.DatabaseURL)
			client, err := mongo.Connect(ctx, clientOpts)
			if err == nil && client.Ping(ctx, nil) == nil {
				mgr.client = client
				mgr.usersCol = client.Database("azml").Collection("users")
				mgr.isMongo = true
				log.Println("[DB] Connected to MongoDB successfully")
			} else {
				log.Printf("[DB] MongoDB connection failed (%v), falling back to local file", err)
			}
		}

		if !mgr.isMongo {
			log.Println("[DB] Using local JSON storage for user persistence")
		}

		DB = mgr
	})
	return DB
}

func (d *DatabaseManager) LoadAllUsers(userMap *types.SafeUserMap) error {
	if d.isMongo {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		cursor, err := d.usersCol.Find(ctx, bson.M{})
		if err != nil {
			return err
		}
		defer cursor.Close(ctx)

		for cursor.Next(ctx) {
			var u types.UserSettings
			if err := cursor.Decode(&u); err == nil {
				userMap.Set(&u)
			}
		}
		return cursor.Err()
	}

	d.fileLock.Lock()
	defer d.fileLock.Unlock()

	data, err := os.ReadFile(d.localFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var users map[int64]*types.UserSettings
	if err := json.Unmarshal(data, &users); err != nil {
		return err
	}

	for _, u := range users {
		userMap.Set(u)
	}
	return nil
}

func (d *DatabaseManager) SaveUser(u *types.UserSettings) error {
	if d.isMongo {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		opts := options.Update().SetUpsert(true)
		_, err := d.usersCol.UpdateOne(ctx, bson.M{"_id": u.UserID}, bson.M{"$set": u}, opts)
		return err
	}

	d.fileLock.Lock()
	defer d.fileLock.Unlock()

	var users map[int64]*types.UserSettings
	data, err := os.ReadFile(d.localFile)
	if err == nil {
		_ = json.Unmarshal(data, &users)
	}
	if users == nil {
		users = make(map[int64]*types.UserSettings)
	}

	users[u.UserID] = u
	newData, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(d.localFile, newData, 0644)
}

