package main

import (
	"context"
	"encoding/json"
	"fmt"

	"log"
	"strconv"

	"math/rand"
	"net/http"

	"github.com/go-playground/validator/v10"

	"github.com/gin-gonic/gin"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/redis/go-redis/v9"
)

type SubjectScore struct {
	Subject string  `json:"subject" bson:"subject" validate:"oneof=chinese english math"`
	Score   float64 `json:"score" bson:"score" binding:"required"`
}
type Student struct {
	Name      string         `json:"name" bson:"name"`
	StudentId string         `json:"student_id" bson:"student_id"`
	Scores    []SubjectScore `json:"scores" bson:"scores"`
}

var subjects = []string{
	"chinese", "english", "math",
}

var mongodb *mongo.Client
var rdb *redis.Client
var ctx = context.Background()

func main() {

	clientOptions := options.Client().ApplyURI("mongodb://localhost:27017")

	var err error
	mongodb, err = mongo.Connect(context.TODO(), clientOptions)

	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		if err = mongodb.Disconnect(context.TODO()); err != nil {
			log.Fatal(err)
		}
	}()

	err = mongodb.Ping(context.TODO(), nil)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Connected to MongoDB!")

	rdb = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379", // Redis 服務器地址
		Password: "",               // 沒有密碼，則設置為空字符串
		DB:       0,                // 使用默認的 DB
	})
	// 檢查連接是否成功
	pong, err := rdb.Ping(ctx).Result()
	fmt.Println(pong, err)

	r := gin.Default()

	r.POST("/student/", AddStudent)
	r.POST("/student/:student_id", EditStudent)
	r.GET("/rank/:subject", GetRank)
	r.GET("/students", GetStudent)

	r.Run(":5566")
}

func GetStudent(c *gin.Context) {
	var result bson.D

	filter := bson.D{{Key: "name", Value: "RiceA"}}

	collection := mongodb.Database("testdb").Collection("users")

	err := collection.FindOne(context.TODO(), filter).Decode(&result)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Found a document: ", result)
}

func AddStudent(c *gin.Context) {
	type postData struct {
		Name string
	}

	var data postData

	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "input error " + err.Error(),
		})
		return
	}

	studentId := insertUser(data.Name)

	c.JSON(http.StatusOK, gin.H{
		"student_id": studentId,
	})
}

func EditStudent(c *gin.Context) {
	studentId := c.Param("student_id")

	// check studentId format

	// check post data

	var studentData Student

	// 使用 Gin 的 ShouldBindJSON 自動綁定 JSON 數據並執行初步驗證
	if err := c.ShouldBindJSON(&studentData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 使用 validator 進行更詳細的驗證

	validate := validator.New()
	if err := validate.Struct(studentData); err != nil {
		validationErrors := err.(validator.ValidationErrors)
		c.JSON(http.StatusBadRequest, gin.H{"validation_errors": validationErrors.Translate(nil)})
		return
	}

	fmt.Printf("post data = %+v\n", studentData)

	// 更新資料
	collection := mongodb.Database("testdb").Collection("users")

	filter := bson.M{"student_id": studentId}
	update := bson.M{
		"$set": bson.M{
			"name":   studentData.Name,
			"scores": studentData.Scores,
		},
	}

	updateResult, err := collection.UpdateOne(context.TODO(), filter, update)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Matched %v documents and updated %v documents.\n", updateResult.MatchedCount, updateResult.ModifiedCount)

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
	})

	// 更新redis

}

func GetRank(c *gin.Context) {
	subject := c.Param("subject")

	numberString := c.DefaultQuery("number", "10")
	number, err := strconv.Atoi(numberString)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "number need to integer",
		})
	}

	fmt.Printf("%s %d\n", subject, number)

	// 搜尋 redis
	/*
		// 添加玩家分數
		addScore("player1", 300)
		addScore("player2", 150)
		addScore("player3", 450)
		addScore("player4", 600)

		// 獲取排行榜前3名
		fmt.Println("Top 3 players:")
		getTopPlayers(rdb, 3)*/
}

func addScore(subject string, student Student, score float64) {
	studentJSON, err := json.Marshal(student)
	if err != nil {
		log.Fatal(err)
	}

	key := "rank_" + subject

	rdb.ZAdd(ctx, key, redis.Z{
		Score:  score,
		Member: studentJSON,
	})
}

func getTopPlayers(subject string, top int64) {
	key := "rank_" + subject

	players, err := rdb.ZRevRangeWithScores(ctx, key, 0, top-1).Result()
	if err != nil {
		fmt.Println("Error fetching top players:", err)
		return
	}

	for i, player := range players {
		fmt.Printf("Rank %d: %s with score %.0f\n", i+1, player.Member, player.Score)
	}
}

func insertUser(name string) string {
	newID, err := getNextSequence("student_id")

	if err != nil {
		log.Fatalf("Failed to get sequence id: %v", err)
	}

	collection := mongodb.Database("testdb").Collection("users")

	newIdString := fmt.Sprintf("R%010d", newID)

	subjectScores := []SubjectScore{}

	for _, sub := range subjects {
		subjectScores = append(subjectScores, SubjectScore{
			sub,
			float64(rand.Intn(101)),
		})
	}

	fmt.Printf("%+v\n", subjectScores)

	student := Student{
		name,
		newIdString,
		subjectScores,
	}

	/*scoreChiness := rand.Intn(101)
	scoreEnglish := rand.Intn(101)
	scoreMath := rand.Intn(101)
	//scoreTotal := scoreChiness + scoreEnglish + scoreMath

	user := bson.D{
		{Key: "student_id", Value: newIdString},
		{Key: "name", Value: name},
		//{Key: "score_total", Value: scoreTotal},
		{Key: "scores", Value: bson.A{
			bson.D{
				{Key: "subject", Value: "chinese"},
				{Key: "score", Value: scoreChiness}},
			bson.D{
				{Key: "subject", Value: "english"},
				{Key: "score", Value: scoreEnglish}},
			bson.D{
				{Key: "subject", Value: "math"},
				{Key: "score", Value: scoreMath}}}}}*/

	studentBSON, err := bson.Marshal(student)
	if err != nil {
		log.Fatal("Error marshaling to BSON:", err)
	}

	insertResult, err := collection.InsertOne(context.TODO(), studentBSON)
	if err != nil {
		log.Fatalf("Failed to insert user: %v", err)
	}

	fmt.Println("Inserted user with ID:", insertResult.InsertedID)

	//return insertResult.
	/*
		addScore("chinese", student, float64(scoreChiness))
		addScore("english", student, float64(scoreEnglish))
		addScore("math", student, float64(scoreMath))*/

	return newIdString
}

func getNextSequence(name string) (int64, error) {
	collection := mongodb.Database("testdb").Collection("counters")

	filter := bson.D{{Key: "_id", Value: name}}
	update := bson.D{{Key: "$inc", Value: bson.D{{Key: "seq", Value: 1}}}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)

	var updatedDoc struct {
		Seq int64 `bson:"seq"`
	}
	err := collection.FindOneAndUpdate(context.TODO(), filter, update, opts).Decode(&updatedDoc)
	if err != nil {
		return 0, err
	}
	return updatedDoc.Seq, nil
}
