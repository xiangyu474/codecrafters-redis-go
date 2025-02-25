func loadRDBFile() error {
	filePath := filepath.Join(cfg.dir, cfg.dbfilename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("RDB file does not exist, starting with empty database")
			return nil
		}
		return fmt.Errorf("failed to read RDB file: %w", err)
	}

	reader := bufio.NewReader(bytes.NewReader(data))

	// 读取 RDB 头部
	header := make([]byte, 9)
	_, err = io.ReadFull(reader, header)
	if err != nil {
		return fmt.Errorf("failed to read RDB header: %w", err)
	}
	fmt.Printf("RDB Header: %s\n", header)

	var expireTime int64 = 0 // 记录当前 key 的过期时间

	for {
		b, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read next byte: %w", err)
		}
		fmt.Printf("b: 0x%X\n", b)

		switch b {
		case 0xFA: // 解析元数据
			metaKey, err := readStringEncoded(reader)
			if err != nil {
				return fmt.Errorf("failed to read meta key: %w", err)
			}

			firstByte, err := reader.ReadByte()
			if err != nil {
				return fmt.Errorf("failed to read next byte: %w", err)
			}

			if firstByte>>6 == 3 { // 判断是否是整数类型
				skipBytes := map[byte]int{0xC0: 1, 0xC1: 2, 0xC2: 4, 0xC3: 8}[firstByte]
				_, err := reader.Discard(skipBytes)
				if err != nil {
					return fmt.Errorf("failed to skip non-string meta value: %w", err)
				}
			} else {
				reader.UnreadByte()
				metaValue, err := readStringEncoded(reader)
				if err != nil {
					return fmt.Errorf("failed to read meta value: %w", err)
				}
				fmt.Printf("Meta: %s = %s\n", metaKey, metaValue)
			}

		case 0xFE: // 切换数据库
			dbIndex, err := readSizeEncoded(reader)
			if err != nil {
				return fmt.Errorf("failed to read db index: %w", err)
			}
			fmt.Printf("Switching to DB %d\n", dbIndex)

		case 0xFB: // 读取哈希表大小信息
			hashTablesize, _ := readSizeEncoded(reader)
			hashTableExpire, _ := readSizeEncoded(reader)
			fmt.Printf("Hash table size: %d, Expire size: %d\n", hashTablesize, hashTableExpire)

		case 0xFF: // 结束 RDB 解析
			return nil

		case 0xFC, 0xFD: // 处理过期时间
			if b == 0xFC { // 8 字节，毫秒
				err := binary.Read(reader, binary.LittleEndian, &expireTime)
				if err != nil {
					return fmt.Errorf("failed to read expiration time: %w", err)
				}
			} else if b == 0xFD { // 4 字节，秒
				var expireSec int32
				err := binary.Read(reader, binary.LittleEndian, &expireSec)
				if err != nil {
					return fmt.Errorf("failed to read expiration time: %w", err)
				}
				expireTime = int64(expireSec) * 1000 // 转换为毫秒
			}
			fmt.Printf("Next key will expire at: %d ms\n", expireTime)

		default: // 读取键值对
			reader.UnreadByte()
			key, err := readStringEncoded(reader)
			if err != nil {
				return fmt.Errorf("failed to read key: %w", err)
			}

			value, err := readStringEncoded(reader)
			if err != nil {
				return fmt.Errorf("failed to read value: %w", err)
			}

			fmt.Printf("Key: %s, Value: %s, Expiration: %d\n", key, value, expireTime)

			// 存储键值对，带上过期时间
			mu.Lock()
			kvStore[key] = entry{value: value, expiration: expireTime}
			mu.Unlock()

			// 处理完 key-value 之后，重置 `expireTime`，防止影响下一个 key
			expireTime = 0
		}
	}
	return nil
}
