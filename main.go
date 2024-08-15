package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"log"
	"strconv"

	"math/rand"
	"net/http"

	"github.com/gin-gonic/gin"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/redis/go-redis/v9"
)

type Student struct {
	Name      string `json:"name" bson:"name"`
	StudentId string `json:"student_id" bson:"student_id"`
	Scores    map[string]float64
}

var mongodb *mongo.Client
var rdb *redis.Client
var ctx = context.Background()

const (
	collectionName string = "students"
	rankListKey    string = "ranklist_"
	hashKey        string = "student_data_"
	scoreKey       string = "student_score_"
)

var subjects = []string{"chinese", "english", "math"}

func main() {

	fmt.Printf("ctx = %+v\n", ctx)

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
	fmt.Printf("%+v\n", c)

	studentIdsString := c.Query("student_id")

	if studentIdsString == "" {
		c.JSON(http.StatusBadRequest, gin.H{})
	}

	studentIds := strings.Split(studentIdsString, ",")
	sort.Strings(studentIds)

	students := make([]map[string]interface{}, 0)

	for _, studentId := range studentIds {
		student := make(map[string]interface{})

		fmt.Printf("Search RDS Hash %s\n", hashKey+studentId)
		result, err := rdb.HGetAll(ctx, hashKey+studentId).Result()
		if err != nil {
			log.Fatal(err.Error())
		}

		if len(result) == 0 {
			// 從mongo中搜尋
			student = getStudentFromMongo(studentId)
			if len(student) == 0 {
				continue
			}

		} else {

			for k, v := range result {
				student[k] = v
			}

			result, err = rdb.HGetAll(ctx, scoreKey+studentId).Result()
			if err != nil {
				log.Fatal(err.Error())
			}
			if len(result) == 0 {
				student = getStudentFromMongo(studentId)
				if len(student) == 0 {
					continue
				}
			} else {
				student["scores"] = result
			}
		}

		ranks := make(map[string]int64)
		for _, v := range subjects {
			ranks[v] = getRankNum(v, studentId)
		}
		ranks["total"] = getRankNum("total", studentId)
		student["rank"] = ranks

		students = append(students, student)
	}

	c.JSON(http.StatusOK, gin.H{
		"result": students,
	})
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

	// 取得新的ID
	newID, err := getNextSequence("student_id")

	if err != nil {
		log.Fatalf("Failed to get sequence id: %v", err)
	}

	collection := mongodb.Database("testdb").Collection(collectionName)

	newIdString := fmt.Sprintf("R%010d", newID)

	scoreChinese := float64(rand.Intn(101))
	scoreEnglish := float64(rand.Intn(101))
	scoreMath := float64(rand.Intn(101))
	scoreTotal := scoreChinese + scoreEnglish + scoreMath

	student := Student{
		Name:      data.Name,
		StudentId: newIdString,
		Scores: map[string]float64{
			"chinese": scoreChinese,
			"english": scoreEnglish,
			"math":    scoreMath,
			"total":   scoreTotal,
		},
	}

	studentBSON, err := bson.Marshal(student)
	if err != nil {
		log.Fatal("Error marshaling to BSON:", err)
	}

	insertResult, err := collection.InsertOne(context.TODO(), studentBSON)
	if err != nil {
		log.Fatalf("Failed to insert user: %v", err)
	}

	fmt.Println("Inserted user with ID:", insertResult.InsertedID)

	addToRedis(student)

	c.JSON(http.StatusOK, gin.H{
		"student_id": newIdString,
	})
}

func addToRedis(student Student) {
	rdb.HSet(ctx, hashKey+student.StudentId,
		"name", student.Name,
		"student_id", student.StudentId,
		//"score_toal", student.ScoresTotal,
	)

	rdb.HSet(ctx, scoreKey+student.StudentId,
		"chinese", student.Scores["chinese"],
		"english", student.Scores["english"],
		"math", student.Scores["math"],
		"total", student.Scores["total"],
	)

	rdb.ZAdd(ctx, rankListKey+"chinese", redis.Z{Score: student.Scores["chinese"], Member: student.StudentId})
	rdb.ZAdd(ctx, rankListKey+"english", redis.Z{Score: student.Scores["english"], Member: student.StudentId})
	rdb.ZAdd(ctx, rankListKey+"math", redis.Z{Score: student.Scores["math"], Member: student.StudentId})
	rdb.ZAdd(ctx, rankListKey+"total", redis.Z{Score: student.Scores["total"], Member: student.StudentId})
}

func EditStudent(c *gin.Context) {
	studentId := c.Param("student_id")

	// check studentId format
	if len(studentId) != 11 || []rune(studentId)[0] != 'R' {
		c.JSON(http.StatusBadRequest, gin.H{"error": "student_id format is error."})
	}

	// check post data
	var updateData map[string]int64
	if err := c.BindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "data format is error"})
		return
	}
	fmt.Printf("post data = %+v\n", updateData)

	var student Student
	filter := bson.M{"student_id": studentId}
	collection := mongodb.Database("testdb").Collection(collectionName)
	err := collection.FindOne(context.TODO(), filter).Decode(&student)
	if err != nil {
		// log.Fatal(err)
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "no student",
		})
		return
	}

	// 只更新成績
	for key, value := range updateData {
		fmt.Printf("key = %s\n", key)

		if !slices.Contains(subjects, key) {
			fmt.Println("not contain " + key)
			continue
		}

		student.Scores[key] = float64(value)

		rdb.HSet(ctx, scoreKey+student.StudentId,
			key, value,
		)
		rdb.ZAdd(ctx, rankListKey+key, redis.Z{Score: float64(value), Member: student.StudentId})
	}

	fmt.Printf("%+v\n", student)

	var total float64 = 0
	for _, v := range subjects {
		total += student.Scores[v]
	}
	student.Scores["total"] = total
	rdb.HSet(ctx, scoreKey+student.StudentId,
		"total", student.Scores["total"],
	)
	rdb.ZAdd(ctx, rankListKey+"total", redis.Z{Score: student.Scores["total"], Member: student.StudentId})

	update := bson.M{
		"$set": bson.M{
			"scores": student.Scores,
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
	ranklist := getTopPlayers(subject, int64(number))

	c.JSON(http.StatusOK, gin.H{
		"result": ranklist,
	})
}

func getTopPlayers(subject string, top int64) map[int]map[string]interface{} {
	key := rankListKey + subject

	fmt.Println("GET " + key)

	players, err := rdb.ZRevRangeWithScores(ctx, key, 0, top-1).Result()
	if err != nil {
		log.Fatal(err.Error())
	}

	fmt.Printf("list number = %d\n", len(players))

	rankList := make(map[int]map[string]interface{})

	for i, player := range players {

		rankList[i+1] = map[string]interface{}{
			"student_id": player.Member,
			"score":      player.Score,
		}
	}

	return rankList
}

func getRankNum(subject string, studentId string) int64 {
	key := rankListKey + subject
	rank, err := rdb.ZRevRank(ctx, key, studentId).Result()
	if err != nil {
		fmt.Println("Error:", err)
	}
	return rank + 1
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

func getStudentFromMongo(studentId string) map[string]interface{} {
	student := make(map[string]interface{})

	// 從mongo中搜尋
	collection := mongodb.Database("testdb").Collection(collectionName)
	filter := bson.D{{Key: "student_id", Value: studentId}}
	var result Student
	err := collection.FindOne(context.TODO(), filter).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return student
		} else {
			log.Fatal(err)
		}
	}
	// 把資料寫到redis
	addToRedis(result)

	student["name"] = result.Name
	student["student_id"] = result.StudentId
	student["scores"] = result.Scores

	return student
}
