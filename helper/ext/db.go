package ext

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"

	"azml-go/config"
	"azml-go/types"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DatabaseManager struct {
	client            *mongo.Client
	usersCol          *mongo.Collection
	settingsCol       *mongo.Collection
	isMongo           bool
	localFile         string
	localSettingsFile string
	fileLock          sync.Mutex
}

var (
	DB   *DatabaseManager
	once sync.Once
)

func GetDB() *DatabaseManager {
	return DB
}

func InitDB() *DatabaseManager {
	once.Do(func() {
		cfg := config.Get()
		mgr := &DatabaseManager{
			localFile:         "user_data.json",
			localSettingsFile: "bot_settings.json",
		}

		if cfg.DatabaseURL != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			clientOpts := options.Client().ApplyURI(cfg.DatabaseURL)
			client, err := mongo.Connect(ctx, clientOpts)
			if err == nil && client.Ping(ctx, nil) == nil {
				mgr.client = client
				mgr.usersCol = client.Database("azml").Collection("users")
				mgr.settingsCol = client.Database("azml").Collection("settings")
				mgr.isMongo = true
				log.Println("[DB] Connected to MongoDB successfully")
			} else {
				log.Printf("[DB] MongoDB connection failed (%v), falling back to local file", err)
			}
		}

		if !mgr.isMongo {
			log.Println("[DB] Using local JSON storage for user & bot persistence")
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

func (d *DatabaseManager) LoadAllBotSettings() (map[string]string, error) {
	if d == nil {
		return nil, nil
	}
	if d.isMongo {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		cursor, err := d.settingsCol.Find(ctx, bson.M{})
		if err != nil {
			return nil, err
		}
		defer cursor.Close(ctx)

		settings := make(map[string]string)
		for cursor.Next(ctx) {
			var doc struct {
				Key   string `bson:"_id"`
				Value string `bson:"value"`
			}
			if err := cursor.Decode(&doc); err == nil && doc.Key != "" {
				settings[doc.Key] = doc.Value
			}
		}
		return settings, cursor.Err()
	}

	d.fileLock.Lock()
	defer d.fileLock.Unlock()

	data, err := os.ReadFile(d.localSettingsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var settings map[string]string
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func (d *DatabaseManager) SaveBotSetting(key, val string) error {
	if d == nil {
		return nil
	}
	if d.isMongo {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		opts := options.Update().SetUpsert(true)
		_, err := d.settingsCol.UpdateOne(
			ctx,
			bson.M{"_id": key},
			bson.M{"$set": bson.M{"value": val, "updated_at": time.Now()}},
			opts,
		)
		return err
	}

	d.fileLock.Lock()
	defer d.fileLock.Unlock()

	settings := make(map[string]string)
	data, err := os.ReadFile(d.localSettingsFile)
	if err == nil {
		_ = json.Unmarshal(data, &settings)
	}

	if val == "" {
		delete(settings, key)
	} else {
		settings[key] = val
	}

	newData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(d.localSettingsFile, newData, 0644)
}

func (d *DatabaseManager) GetBotSetting(key string) (string, error) {
	if d == nil {
		return "", nil
	}
	if d.isMongo {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var doc struct {
			Key   string `bson:"_id"`
			Value string `bson:"value"`
		}
		err := d.settingsCol.FindOne(ctx, bson.M{"_id": key}).Decode(&doc)
		if err != nil {
			return "", err
		}
		return doc.Value, nil
	}

	settings, err := d.LoadAllBotSettings()
	if err != nil {
		return "", err
	}
	return settings[key], nil
}
