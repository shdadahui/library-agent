package seed

// builtinBooks 内置演示书单：中文经典 + 数学 + 英文经典。
// 包含《三体》《活着》《百年孤独》等演示命中书名，离线即可运行。
var builtinBooks = []SeedBook{
	// 中文科幻
	{Title: "三体", Author: "刘慈欣", Publisher: "重庆出版社", Year: 2008, Subjects: "科幻,小说", Lang: "zh"},
	{Title: "三体Ⅱ·黑暗森林", Author: "刘慈欣", Publisher: "重庆出版社", Year: 2008, Subjects: "科幻,小说", Lang: "zh"},
	{Title: "三体Ⅲ·死神永生", Author: "刘慈欣", Publisher: "重庆出版社", Year: 2010, Subjects: "科幻,小说", Lang: "zh"},
	// 中文文学
	{Title: "活着", Author: "余华", Publisher: "作家出版社", Year: 2012, Subjects: "小说,文学", Lang: "zh"},
	{Title: "许三观卖血记", Author: "余华", Publisher: "作家出版社", Year: 2012, Subjects: "小说,文学", Lang: "zh"},
	{Title: "围城", Author: "钱钟书", Publisher: "人民文学出版社", Year: 1991, Subjects: "小说,讽刺", Lang: "zh"},
	{Title: "平凡的世界", Author: "路遥", Publisher: "北京十月文艺出版社", Year: 2017, Subjects: "小说,现实主义", Lang: "zh"},
	{Title: "白鹿原", Author: "陈忠实", Publisher: "人民文学出版社", Year: 1993, Subjects: "小说,历史", Lang: "zh"},
	{Title: "呐喊", Author: "鲁迅", Publisher: "人民文学出版社", Year: 1973, Subjects: "小说,短篇", Lang: "zh"},
	{Title: "朝花夕拾", Author: "鲁迅", Publisher: "人民文学出版社", Year: 1979, Subjects: "散文,回忆录", Lang: "zh"},
	{Title: "边城", Author: "沈从文", Publisher: "人民文学出版社", Year: 2000, Subjects: "小说,乡土", Lang: "zh"},
	{Title: "骆驼祥子", Author: "老舍", Publisher: "人民文学出版社", Year: 1962, Subjects: "小说,现实主义", Lang: "zh"},
	{Title: "子夜", Author: "茅盾", Publisher: "人民文学出版社", Year: 1960, Subjects: "小说,社会", Lang: "zh"},
	{Title: "蛙", Author: "莫言", Publisher: "上海文艺出版社", Year: 2009, Subjects: "小说,乡土", Lang: "zh"},
	{Title: "红高粱家族", Author: "莫言", Publisher: "浙江文艺出版社", Year: 2020, Subjects: "小说,乡土", Lang: "zh"},
	{Title: "红楼梦", Author: "曹雪芹", Publisher: "人民文学出版社", Year: 1996, Subjects: "古典,四大名著", Lang: "zh"},
	{Title: "西游记", Author: "吴承恩", Publisher: "人民文学出版社", Year: 1955, Subjects: "古典,四大名著", Lang: "zh"},
	{Title: "三国演义", Author: "罗贯中", Publisher: "人民文学出版社", Year: 1973, Subjects: "古典,四大名著", Lang: "zh"},
	{Title: "水浒传", Author: "施耐庵", Publisher: "人民文学出版社", Year: 1975, Subjects: "古典,四大名著", Lang: "zh"},
	{Title: "百年孤独", Author: "加西亚·马尔克斯", Publisher: "南海出版公司", Year: 2011, Subjects: "小说,魔幻现实主义", Lang: "zh"},
	// 数学（用户兴趣）
	{Title: "数学：确定性的丧失", Author: "莫里斯·克莱因", Publisher: "湖南科学技术出版社", Year: 1997, Subjects: "数学,科普", Lang: "zh"},
	{Title: "几何原本", Author: "欧几里得", Publisher: "陕西人民出版社", Year: 2010, Subjects: "数学,经典", Lang: "zh"},
	{Title: "哥德尔、艾舍尔、巴赫", Author: "侯世达", Publisher: "商务印书馆", Year: 1996, Subjects: "数学,逻辑,认知", Lang: "zh"},
	// 英文经典
	{Title: "1984", Author: "George Orwell", Publisher: "Secker & Warburg", Year: 1949, Subjects: "Fiction,Dystopia", Lang: "en"},
	{Title: "Animal Farm", Author: "George Orwell", Publisher: "Secker & Warburg", Year: 1945, Subjects: "Fiction,Allegory", Lang: "en"},
	{Title: "Pride and Prejudice", Author: "Jane Austen", Publisher: "T. Egerton", Year: 1813, Subjects: "Fiction,Romance", Lang: "en"},
	{Title: "The Great Gatsby", Author: "F. Scott Fitzgerald", Publisher: "Charles Scribner's Sons", Year: 1925, Subjects: "Fiction,Classic", Lang: "en"},
	{Title: "To Kill a Mockingbird", Author: "Harper Lee", Publisher: "J.B. Lippincott", Year: 1960, Subjects: "Fiction,Classic", Lang: "en"},
	{Title: "The Catcher in the Rye", Author: "J.D. Salinger", Publisher: "Little, Brown", Year: 1951, Subjects: "Fiction,Classic", Lang: "en"},
	{Title: "Brave New World", Author: "Aldous Huxley", Publisher: "Chatto & Windus", Year: 1932, Subjects: "Fiction,Dystopia", Lang: "en"},
	{Title: "Dune", Author: "Frank Herbert", Publisher: "Chilton Books", Year: 1965, Subjects: "Fiction,Science Fiction", Lang: "en"},
	{Title: "Foundation", Author: "Isaac Asimov", Publisher: "Gnome Press", Year: 1951, Subjects: "Fiction,Science Fiction", Lang: "en"},
	{Title: "Sapiens: A Brief History of Humankind", Author: "Yuval Noah Harari", Publisher: "Harper", Year: 2014, Subjects: "Nonfiction,History", Lang: "en"},
	{Title: "A Brief History of Time", Author: "Stephen Hawking", Publisher: "Bantam Books", Year: 1988, Subjects: "Nonfiction,Science", Lang: "en"},
	{Title: "The Selfish Gene", Author: "Richard Dawkins", Publisher: "Oxford University Press", Year: 1976, Subjects: "Nonfiction,Biology", Lang: "en"},
	{Title: "Crime and Punishment", Author: "Fyodor Dostoevsky", Publisher: "The Russian Messenger", Year: 1866, Subjects: "Fiction,Classic", Lang: "en"},
	{Title: "The Old Man and the Sea", Author: "Ernest Hemingway", Publisher: "Charles Scribner's Sons", Year: 1952, Subjects: "Fiction,Classic", Lang: "en"},
}
